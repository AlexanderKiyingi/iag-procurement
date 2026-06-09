package consumer

import (
	"context"
	"encoding/json"
	"log"

	"github.com/segmentio/kafka-go"

	"iag-procurement/backend/internal/repo"
)

type OperationsConfig struct {
	Brokers []string
	GroupID string
	Topic   string
}

type Operations struct {
	cfg  OperationsConfig
	repo *repo.Procurement
}

func NewOperations(cfg OperationsConfig, p *repo.Procurement) *Operations {
	return &Operations{cfg: cfg, repo: p}
}

func (c *Operations) Run(ctx context.Context) error {
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

	log.Printf("procurement operations consumer started topic=%s group=%s", c.cfg.Topic, c.cfg.GroupID)
	for {
		msg, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("procurement operations fetch: %v", err)
			continue
		}
		if err := c.handle(ctx, msg.Value); err != nil {
			log.Printf("procurement operations handle: %v", err)
			continue
		}
		if err := r.CommitMessages(ctx, msg); err != nil {
			log.Printf("procurement operations commit: %v", err)
		}
	}
}

type opsEnvelope struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func (c *Operations) handle(ctx context.Context, raw []byte) error {
	var env opsEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	if env.Type != "warehouse.stock.below_minimum" {
		return nil
	}
	return c.repo.RecordLowStockSignal(ctx, env.Data)
}
