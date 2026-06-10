package model

import "time"

type AuditDecisionType string

const (
	AuditDecisionAllocate     AuditDecisionType = "allocate"
	AuditDecisionPreempt      AuditDecisionType = "preempt"
	AuditDecisionShare        AuditDecisionType = "share"
	AuditDecisionQueue        AuditDecisionType = "queue"
	AuditDecisionDowngrade    AuditDecisionType = "downgrade"
	AuditDecisionReprioritize AuditDecisionType = "reprioritize"
	AuditDecisionBlocked      AuditDecisionType = "blocked"
	AuditDecisionUnblocked    AuditDecisionType = "unblocked"
	AuditDecisionSkipped      AuditDecisionType = "skipped"
	AuditDecisionCycleDetect  AuditDecisionType = "cycle_detect"
	AuditDecisionDepTimeout   AuditDecisionType = "dep_timeout"
	AuditDecisionDepAdd       AuditDecisionType = "dep_add"
	AuditDecisionDepRemove    AuditDecisionType = "dep_remove"
	AuditDecisionDAGExport    AuditDecisionType = "dag_export"
	AuditDecisionDAGImport    AuditDecisionType = "dag_import"
	AuditDecisionDAGSnapshot  AuditDecisionType = "dag_snapshot"
	AuditDecisionDAGRestore   AuditDecisionType = "dag_restore"
)

type AuditRecord struct {
	Timestamp    time.Time         `json:"timestamp"`
	DecisionType AuditDecisionType `json:"decision_type"`
	TaskID       string            `json:"task_id"`
	GPUs         []string          `json:"gpus,omitempty"`
	Reason       string            `json:"reason"`
	Extra        map[string]string `json:"extra,omitempty"`
}
