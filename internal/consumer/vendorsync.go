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

// vendorUpserted is the canonical cross-service vendor master event. Finance
// emits it on iag.finance; procurement ingests it to keep its own vendor master
// in step, keyed on the shared party_id. (SCM parties arrive via the separate
// supply-chain consumer.)
const vendorUpserted = "party.vendor.upserted"

// procurementSource is procurement's own CloudEvents source; events stamped with
// it are skipped so procurement never re-ingests what it emitted.
const procurementSource = "iag.procurement"

// VendorSync consumes party.vendor.upserted from a peer service's topic and
// upserts the local vendor master. It never re-emits — that is the mesh's
// loop-prevention rule.
type VendorSync struct {
	reader      *kafka.Reader
	procurement *repo.Procurement
}

func NewVendorSync(cfg Config, procurement *repo.Procurement) *VendorSync {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    cfg.Topic,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &VendorSync{reader: reader, procurement: procurement}
}

func (c *VendorSync) Run(ctx context.Context) error {
	log.Printf("procurement vendor-sync consumer started topic=%s group=%s", c.reader.Config().Topic, c.reader.Config().GroupID)
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement vendor-sync fetch: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if err := c.handleMessage(ctx, msg); err != nil {
			log.Printf("procurement vendor-sync handle: %v", err)
		} else if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement vendor-sync commit: %v", err)
		}
	}
}

func (c *VendorSync) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

type vendorUpsertData struct {
	PartyID  string `json:"party_id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Country  string `json:"country"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

func (c *VendorSync) handleMessage(ctx context.Context, msg kafka.Message) error {
	var evt PlatformEvent
	if err := json.Unmarshal(msg.Value, &evt); err != nil {
		return err
	}
	if evt.Type != vendorUpserted {
		return nil
	}
	// Defensive loop guard: never ingest our own emissions.
	if strings.EqualFold(strings.TrimSpace(evt.Source), procurementSource) {
		return nil
	}

	eventID := evt.ID
	if eventID == "" {
		eventID = string(msg.Key)
	}
	ok, err := c.procurement.MarkKafkaEventProcessed(ctx, eventID, msg.Topic)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	var data vendorUpsertData
	if err := json.Unmarshal(evt.Data, &data); err != nil {
		return err
	}
	return c.procurement.UpsertVendorByParty(ctx, data.PartyID, data.Code, data.Name,
		data.Category, data.Email, data.Phone, data.Country, data.Status)
}
