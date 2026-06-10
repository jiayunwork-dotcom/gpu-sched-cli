package store

import (
	"sync"
	"time"

	"github.com/gpu-sched-cli/internal/model"
)

const maxAuditRecords = 10000

type AuditLogger struct {
	mu      sync.RWMutex
	records []*model.AuditRecord
}

func NewAuditLogger() *AuditLogger {
	return &AuditLogger{
		records: make([]*model.AuditRecord, 0),
	}
}

func (al *AuditLogger) Record(decisionType model.AuditDecisionType, taskID string, gpus []string, reason string, extra map[string]string) {
	al.mu.Lock()
	defer al.mu.Unlock()

	record := &model.AuditRecord{
		Timestamp:    time.Now(),
		DecisionType: decisionType,
		TaskID:       taskID,
		GPUs:         gpus,
		Reason:       reason,
		Extra:        extra,
	}
	al.records = append(al.records, record)

	if len(al.records) > maxAuditRecords {
		al.records = al.records[len(al.records)-maxAuditRecords:]
	}
}

func (al *AuditLogger) GetRecords(n int) []*model.AuditRecord {
	al.mu.RLock()
	defer al.mu.RUnlock()

	if n <= 0 || n > len(al.records) {
		n = len(al.records)
	}
	result := make([]*model.AuditRecord, n)
	start := len(al.records) - n
	for i := 0; i < n; i++ {
		result[i] = al.records[start+i]
	}
	return result
}

func (al *AuditLogger) Filter(taskID string, decisionType model.AuditDecisionType, n int) []*model.AuditRecord {
	al.mu.RLock()
	defer al.mu.RUnlock()

	all := al.GetRecords(len(al.records))
	var filtered []*model.AuditRecord

	for i := len(all) - 1; i >= 0; i-- {
		r := all[i]
		if taskID != "" && r.TaskID != taskID {
			continue
		}
		if decisionType != "" && r.DecisionType != decisionType {
			continue
		}
		filtered = append(filtered, r)
		if len(filtered) >= n {
			break
		}
	}

	return filtered
}

func (al *AuditLogger) AllRecords() []*model.AuditRecord {
	al.mu.RLock()
	defer al.mu.RUnlock()
	result := make([]*model.AuditRecord, len(al.records))
	copy(result, al.records)
	return result
}

func (al *AuditLogger) SetRecords(records []*model.AuditRecord) {
	al.mu.Lock()
	defer al.mu.Unlock()
	al.records = records
}

func (al *AuditLogger) RecordsSince(since time.Time) []*model.AuditRecord {
	al.mu.RLock()
	defer al.mu.RUnlock()

	var result []*model.AuditRecord
	for _, r := range al.records {
		if r.Timestamp.After(since) {
			result = append(result, r)
		}
	}
	return result
}
