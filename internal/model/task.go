package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type TaskStatus string

const (
	TaskStatusSubmitted    TaskStatus = "submitted"
	TaskStatusQueued       TaskStatus = "queued"
	TaskStatusScheduling   TaskStatus = "scheduling"
	TaskStatusRunning      TaskStatus = "running"
	TaskStatusCompleted    TaskStatus = "completed"
	TaskStatusFailed       TaskStatus = "failed"
	TaskStatusTimedOut     TaskStatus = "timed_out"
	TaskStatusPreempted    TaskStatus = "preempted"
	TaskStatusBlocked      TaskStatus = "blocked"
	TaskStatusSkipped      TaskStatus = "skipped"
	TaskStatusCancelled    TaskStatus = "cancelled"
)

type DepCondition string

const (
	DepConditionCompleted     DepCondition = "completed"
	DepConditionSuccessOrSkip DepCondition = "success_or_skip"
	DepConditionAnyTerminal   DepCondition = "any_terminal"
)

type DependencySpec struct {
	Task      string       `yaml:"task" json:"task"`
	Condition DepCondition `yaml:"condition,omitempty" json:"condition,omitempty"`
	Weight    int          `yaml:"weight,omitempty" json:"weight,omitempty"`
	Timeout   int          `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

func (d *DependencySpec) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var taskStr string
	if err := unmarshal(&taskStr); err == nil {
		*d = ParseDependencySpec(taskStr)
		return nil
	}

	type plainDep DependencySpec
	var plain plainDep
	if err := unmarshal(&plain); err != nil {
		return err
	}
	*d = DependencySpec(plain)
	if d.Condition == "" {
		d.Condition = DepConditionCompleted
	}
	if d.Weight <= 0 {
		d.Weight = 1
	}
	return nil
}

func ParseDependencySpec(s string) DependencySpec {
	spec := DependencySpec{
		Condition: DepConditionCompleted,
		Weight:    1,
		Timeout:   0,
	}

	parts := strings.SplitN(s, ":", 2)
	spec.Task = parts[0]

	if len(parts) > 1 {
		condStr := parts[1]
		switch strings.ToLower(condStr) {
		case "completed":
			spec.Condition = DepConditionCompleted
		case "success_or_skip":
			spec.Condition = DepConditionSuccessOrSkip
		case "any_terminal":
			spec.Condition = DepConditionAnyTerminal
		default:
			spec.Condition = DepConditionCompleted
		}
	}

	return spec
}

func (d DependencySpec) String() string {
	if d.Condition == DepConditionCompleted && d.Weight == 1 && d.Timeout == 0 {
		return d.Task
	}
	return fmt.Sprintf("%s:%s", d.Task, string(d.Condition))
}

type GPURequirement struct {
	MinCount    int      `yaml:"min_count" json:"min_count"`
	MaxCount    int      `yaml:"max_count" json:"max_count"`
	MinMemory   int      `yaml:"min_memory_gb" json:"min_memory_gb"`
	PreferModel GPUModel `yaml:"prefer_model" json:"prefer_model"`
}

type TaskSpec struct {
	Name            string           `yaml:"name" json:"name"`
	GPUReq          GPURequirement   `yaml:"gpu" json:"gpu"`
	CPUReq          int              `yaml:"cpu_cores" json:"cpu_cores"`
	MemoryReq       int              `yaml:"memory_gb" json:"memory_gb"`
	EstimatedMin    int              `yaml:"estimated_minutes" json:"estimated_minutes"`
	Priority        int              `yaml:"priority" json:"priority"`
	MultiCardComm   bool             `yaml:"multi_card_comm" json:"multi_card_comm"`
	Affinity        string           `yaml:"affinity" json:"affinity"`
	AntiAffinity    string           `yaml:"anti_affinity" json:"anti_affinity"`
	User            string           `yaml:"user" json:"user"`
	DependsOn       []DependencySpec `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
}

func (t *TaskSpec) DependsOnTaskNames() []string {
	names := make([]string, len(t.DependsOn))
	for i, d := range t.DependsOn {
		names[i] = d.Task
	}
	return names
}

func ParseDepCondition(cond string) DepCondition {
	switch strings.ToLower(cond) {
	case "success_or_skip":
		return DepConditionSuccessOrSkip
	case "any_terminal":
		return DepConditionAnyTerminal
	case "completed", "":
		return DepConditionCompleted
	default:
		return DepConditionCompleted
	}
}

func AtoiSafe(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

type Task struct {
	ID              string        `yaml:"-" json:"id"`
	Spec            TaskSpec      `yaml:"-" json:"spec"`
	Status          TaskStatus    `yaml:"-" json:"status"`
	AllocatedGPUs   []string      `yaml:"-" json:"allocated_gpus"`
	GPUCount        int           `yaml:"-" json:"gpu_count"`
	CrossNode       bool          `yaml:"-" json:"cross_node"`
	SubmittedAt     time.Time     `yaml:"-" json:"submitted_at"`
	StartedAt       *time.Time    `yaml:"-" json:"started_at"`
	FinishedAt      *time.Time    `yaml:"-" json:"finished_at"`
	RetryCount      int           `yaml:"-" json:"retry_count"`
	NextRetryAt     *time.Time    `yaml:"-" json:"next_retry_at"`
	QueueEnterAt    time.Time     `yaml:"-" json:"queue_enter_at"`
	PreemptedFrom   []string      `yaml:"-" json:"preempted_from"`
	GangWaitStart   *time.Time    `yaml:"-" json:"gang_wait_start"`
}

func (t *Task) EffectivePriority() int {
	return t.Spec.Priority
}

func (t *Task) QueueLevel() int {
	switch {
	case t.Spec.Priority >= 8:
		return 0 // high
	case t.Spec.Priority >= 4:
		return 1 // medium
	default:
		return 2 // low
	}
}

func (t *Task) QueueName() string {
	switch t.QueueLevel() {
	case 0:
		return "high"
	case 1:
		return "medium"
	default:
		return "low"
	}
}

func (t *Task) Runtime() time.Duration {
	if t.StartedAt == nil {
		return 0
	}
	end := time.Now()
	if t.FinishedAt != nil {
		end = *t.FinishedAt
	}
	return end.Sub(*t.StartedAt)
}

func (t *Task) WaitTime() time.Duration {
	return time.Since(t.QueueEnterAt)
}

func (t *Task) IsGangScheduling() bool {
	return t.Spec.GPUReq.MinCount > 1 || t.Spec.MultiCardComm
}

func (t *Task) NeedsSharedGPU() bool {
	return t.Spec.GPUReq.MinMemory < t.Spec.GPUReq.MinCount*50/2
}

func (t *Task) CanShareGPU() bool {
	perGPUMem := t.Spec.GPUReq.MinMemory / t.Spec.GPUReq.MinCount
	return perGPUMem < 50
}
