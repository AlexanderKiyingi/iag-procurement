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
const financePaymentMade = "finance.payment.made"

type FinanceConfig struct {
	Brokers []string
	GroupID string
	Topic   string
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

	log.Printf("procurement finance consumer started topic=%s group=%s", c.cfg.Topic, c.cfg.GroupID)
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
			// restart. RecordVendorPayment is idempotent (unique payment ref).
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

func (c *Finance) handle(ctx context.Context, raw []byte) error {
	var env financeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Type != financePaymentMade {
		return nil
	}

	// Idempotency: skip events already reflected (belt-and-suspenders alongside
	// the payments.reference unique index).
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
