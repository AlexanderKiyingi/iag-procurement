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

const (
	scmPartyCreated      = "scm.party.created"
	scmPartyUpdated      = "scm.party.updated"
	scmPartyPortalLinked = "scm.party.portal_linked"
)

// SupplyChain consumes iag.supply-chain for party sync (Phase 4.6).
type SupplyChain struct {
	reader      *kafka.Reader
	procurement *repo.Procurement
}

func NewSupplyChain(cfg Config, procurement *repo.Procurement) *SupplyChain {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		GroupID:  cfg.GroupID,
		Topic:    cfg.Topic,
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	return &SupplyChain{reader: reader, procurement: procurement}
}

func (c *SupplyChain) Run(ctx context.Context) error {
	log.Printf("procurement supply-chain consumer started topic=%s group=%s", c.reader.Config().Topic, c.reader.Config().GroupID)
	for {
		msg, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement supply-chain fetch: %v", err)
			time.Sleep(time.Second)
			continue
		}
		if err := c.handleMessage(ctx, msg); err != nil {
			log.Printf("procurement supply-chain handle: %v", err)
		} else if err := c.reader.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement supply-chain commit: %v", err)
		}
	}
}

func (c *SupplyChain) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

type scmPartyData struct {
	PartyID         string `json:"party_id"`
	PartyBusinessID string `json:"party_business_id"`
	SupplierType    string `json:"supplier_type"`
	Name            string `json:"name"`
}

func (c *SupplyChain) handleMessage(ctx context.Context, msg kafka.Message) error {
	var evt PlatformEvent
	if err := json.Unmarshal(msg.Value, &evt); err != nil {
		return err
	}
	switch evt.Type {
	case scmPartyCreated, scmPartyUpdated:
	case scmPartyPortalLinked:
	default:
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

	if evt.Type == scmPartyPortalLinked {
		var data struct {
			PartyID         string `json:"party_id"`
			PartyBusinessID string `json:"party_business_id"`
			PlatformUserID  string `json:"platform_user_id"`
		}
		if err := json.Unmarshal(evt.Data, &data); err != nil {
			return err
		}
		return c.procurement.LinkPortalUser(ctx, data.PartyID, data.PartyBusinessID, data.PlatformUserID)
	}

	var data scmPartyData
	if err := json.Unmarshal(evt.Data, &data); err != nil {
		return err
	}
	businessID := strings.TrimSpace(data.PartyBusinessID)
	if businessID == "" {
		businessID = strings.TrimSpace(data.PartyID)
	}
	return c.procurement.SyncSCMParty(ctx, data.PartyID, businessID, data.SupplierType, data.Name)
}
