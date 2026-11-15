package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"github.com/segmentio/kafka-go"

	"iag-procurement/backend/internal/repo"
)

// financePaymentMade is emitted by iag-finance when an open item is settled.
// Procurement subscribes to iag.finance and, for AP (vendor) payments, reflects
// the settlement back onto the originating invoice + PO so its read model stops
// showing a stale "Not Issued" / "Pending Approval" state.
const (
	financePaymentMade   = "finance.payment.made"
	financeBudgetUpserted = "finance.budget.upserted"
)

type FinanceConfig struct {
	Brokers []string
	GroupID string
	Topic   string
	// PaymentWriteback enables finance.payment.made → vendor payment reflection.
	// Budget mirror (finance.budget.upserted) is always on when this consumer runs.
	PaymentWriteback bool
}

type Finance struct {
	cfg  FinanceConfig
	repo *repo.Procurement
}

func NewFinance(cfg FinanceConfig, p *repo.Procurement) *Finance {
	return &Finance{cfg: cfg, repo: p}
}

func (c *Finance) Run(ctx context.Context) error {
	if len(c.cfg.Brokers) == 0 {
		return nil
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  c.cfg.Brokers,
		GroupID:  c.cfg.GroupID,
		Topic:    c.cfg.Topic,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	defer r.Close()

	log.Printf("procurement finance consumer started topic=%s group=%s paymentWriteback=%v",
		c.cfg.Topic, c.cfg.GroupID, c.cfg.PaymentWriteback)
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement finance fetch: %v", err)
			continue
		}
		if err := c.handle(ctx, msg.Value); err != nil {
			// At-least-once: leave uncommitted so a transient failure retries on
			// restart. Handlers are idempotent (processed_events / FIN code).
			log.Printf("procurement finance handle: %v", err)
			continue
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement finance commit: %v", err)
		}
	}
}

type financeEnvelope struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

// paymentMadeData mirrors finance's paymentMadeOutbox payload. amount arrives as
// a decimal string; documentRef is the finance AP document_ref, which for a
// procurement-sourced AP item is the procurement invoice id/number.
type paymentMadeData struct {
	Direction   string `json:"direction"`
	OpenItemID  string `json:"openItemId"`
	DocumentRef string `json:"documentRef"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	PaymentRef  string `json:"paymentRef"`
}

type budgetUpsertedData struct {
	EntityID       string `json:"entityId"`
	Period         string `json:"period"`
	AccountCode    string `json:"accountCode"`
	Amount         string `json:"amount"`
	CostCenterID   string `json:"costCenterId"`
	CostCenterCode string `json:"costCenterCode"`
}

func (c *Finance) handle(ctx context.Context, raw []byte) error {
	var env financeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	switch env.Type {
	case financePaymentMade:
		if !c.cfg.PaymentWriteback {
			return nil
		}
		return c.handlePaymentMade(ctx, env)
	case financeBudgetUpserted:
		return c.handleBudgetUpserted(ctx, env)
	default:
		return nil
	}
}

func (c *Finance) handlePaymentMade(ctx context.Context, env financeEnvelope) error {
	if env.ID != "" {
		if done, err := c.repo.IsEventProcessed(ctx, env.ID); err != nil {
			return err
		} else if done {
			return nil
		}
	}

	var d paymentMadeData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return err
	}
	// Only vendor (AP) payments concern procurement; AR settlements are sales.
	if !strings.EqualFold(strings.TrimSpace(d.Direction), "ap") {
		return c.repo.MarkEventProcessed(ctx, env.ID)
	}
	invoiceRef := strings.TrimSpace(d.DocumentRef)
	if invoiceRef == "" {
		return c.repo.MarkEventProcessed(ctx, env.ID) // no ref to correlate — not retryable
	}
	amount, _ := strconv.ParseFloat(strings.TrimSpace(d.Amount), 64)

	if err := c.repo.RecordVendorPayment(ctx, invoiceRef, strings.TrimSpace(d.PaymentRef), amount, strings.TrimSpace(d.Currency), "", nil); err != nil {
		return err
	}
	log.Printf("procurement: reflected finance payment ref=%s for invoice=%s", strings.TrimSpace(d.PaymentRef), invoiceRef)
	return c.repo.MarkEventProcessed(ctx, env.ID)
}

func (c *Finance) handleBudgetUpserted(ctx context.Context, env financeEnvelope) error {
	if env.ID != "" {
		if done, err := c.repo.IsEventProcessed(ctx, env.ID); err != nil {
			return err
		} else if done {
			return nil
		}
	}

	var d budgetUpsertedData
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return err
	}
	cc := strings.TrimSpace(d.CostCenterCode)
	if cc == "" && strings.TrimSpace(d.CostCenterID) == "" {
		// Entity-wide finance plan lines have no department envelope to mirror.
		return c.repo.MarkEventProcessed(ctx, env.ID)
	}
	if cc == "" {
		cc = strings.TrimSpace(d.CostCenterID) // last resort; ResolveBudgetForDept wants a code
	}
	cc = repo.CanonicalDeptCode(cc)
	amount, err := strconv.ParseFloat(strings.TrimSpace(d.Amount), 64)
	if err != nil {
		log.Printf("procurement: dropping finance budget with bad amount %q", d.Amount)
		return c.repo.MarkEventProcessed(ctx, env.ID)
	}
	period := strings.TrimSpace(d.Period)
	account := strings.TrimSpace(d.AccountCode)
	entity := strings.TrimSpace(d.EntityID)
	if period == "" || account == "" {
		return c.repo.MarkEventProcessed(ctx, env.ID)
	}
	ccKey := cc
	if ccKey == "" {
		ccKey = "_"
	}
	code := "FIN:" + entity + ":" + period + ":" + account + ":" + ccKey
	b, err := c.repo.UpsertBudgetFromFinance(ctx, code, period, amount, cc)
	if err != nil {
		return err
	}
	log.Printf("procurement: mirrored finance budget code=%s dept=%s allocated=%.2f id=%s", code, cc, amount, b.ID)
	if env.ID != "" {
		return c.repo.MarkEventProcessed(ctx, env.ID)
	}
	return nil
}
