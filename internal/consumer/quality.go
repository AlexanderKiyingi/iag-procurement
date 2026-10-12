package consumer

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"

	"iag-procurement/backend/internal/repo"
)

// qcSupplierScorecard is quality's advice on a supplier (iag-quality-control
// 023), published to iag.quality when its recommended status changes.
const qcSupplierScorecard = "qc.supplier.scorecard"

// QualityAdvice consumes qc.supplier.scorecard and stores the advice beside
// procurement's own quality decision. It never changes that decision.
type QualityAdvice struct {
	reader      *kafka.Reader
	procurement *repo.Procurement
}

func NewQualityAdvice(cfg Config, procurement *repo.Procurement) *QualityAdvice {
	return &QualityAdvice{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: cfg.Brokers, GroupID: cfg.GroupID, Topic: cfg.Topic, MinBytes: 1, MaxBytes: 10e6,
		}),
		procurement: procurement,
	}
}

func (c *QualityAdvice) Run(ctx context.Context) error {
	log.Printf("procurement quality-advice consumer started topic=%s group=%s", c.reader.Config().Topic, c.reader.Config().GroupID)
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement quality-advice fetch: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if err := c.handleMessage(ctx, msg); err != nil {
			// Not committed: the message comes back and is tried again.
			log.Printf("procurement quality-advice handle: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement quality-advice commit: %v", err)
		}
	}
}

func (c *QualityAdvice) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

type scorecardData struct {
	SupplierRef       string   `json:"supplier_ref"`
	RecommendedStatus string   `json:"recommended_status"`
	AcceptanceRate    *float64 `json:"acceptance_rate"`
	Reasons           []string `json:"reasons"`
}

// parseScorecard reads one event; ok is false for anything that is not a
// usable scorecard (another event type, no supplier).
func parseScorecard(raw []byte) (evt PlatformEvent, rec repo.QCRecommendation, ok bool, err error) {
	if err = json.Unmarshal(raw, &evt); err != nil {
		return evt, rec, false, err
	}
	if evt.Type != qcSupplierScorecard {
		return evt, rec, false, nil
	}
	var data scorecardData
	if err = json.Unmarshal(evt.Data, &data); err != nil {
		return evt, rec, false, err
	}
	if strings.TrimSpace(data.SupplierRef) == "" || strings.TrimSpace(data.RecommendedStatus) == "" {
		return evt, rec, false, nil
	}
	at, perr := time.Parse(time.RFC3339, evt.Time)
	if perr != nil {
		at = time.Now().UTC()
	}
	return evt, repo.QCRecommendation{SupplierRef: data.SupplierRef, RecommendedStatus: data.RecommendedStatus,
		AcceptanceRate: data.AcceptanceRate, Reasons: data.Reasons, At: at}, true, nil
}

/*
handleMessage applies the advice, then marks the event processed. Applying is
idempotent (and ignores advice older than what is stored), so a crash between
the two only means the same advice is applied again — whereas marking first
would lose the advice if the update then failed.
*/
func (c *QualityAdvice) handleMessage(ctx context.Context, msg kafka.Message) error {
	evt, rec, ok, err := parseScorecard(msg.Value)
	if err != nil {
		log.Printf("procurement quality-advice: unreadable event at offset %d: %v", msg.Offset, err)
		return nil // a malformed message will never parse; do not block the partition
	}
	if !ok {
		return nil
	}
	matched, err := c.procurement.ApplyQCRecommendation(ctx, rec)
	if err != nil {
		return err
	}
	if !matched {
		log.Printf("procurement quality-advice: no vendor %q for %s", rec.SupplierRef, evt.ID)
	}
	id := evt.ID
	if id == "" {
		id = string(msg.Key)
	}
	if id != "" {
		if _, err := c.procurement.MarkKafkaEventProcessed(ctx, id, msg.Topic); err != nil {
			return err
		}
	}
	return nil
}
