package model

type ScheduleStrategy string

const (
	StrategyBestFit ScheduleStrategy = "best-fit"
	StrategyFirstFit ScheduleStrategy = "first-fit"
)

type SchedulerConfig struct {
	Strategy       ScheduleStrategy   `yaml:"strategy" json:"strategy"`
	PreemptEnabled bool              `yaml:"preempt_enabled" json:"preempt_enabled"`
	SharingEnabled bool              `yaml:"sharing_enabled" json:"sharing_enabled"`
	TimeoutMultiplier float64        `yaml:"timeout_multiplier" json:"timeout_multiplier"`
	MaxRetries     int               `yaml:"max_retries" json:"max_retries"`
	GangWaitTimeoutMin int           `yaml:"gang_wait_timeout_min" json:"gang_wait_timeout_min"`
	QueueWeights   map[string]float64 `yaml:"queue_weights" json:"queue_weights"`
	UserQuotas     map[string]float64 `yaml:"user_quotas" json:"user_quotas"`
	TimeoutEnabled bool              `yaml:"timeout_enabled" json:"timeout_enabled"`
}

func DefaultSchedulerConfig() *SchedulerConfig {
	return &SchedulerConfig{
		Strategy:       StrategyBestFit,
		PreemptEnabled: true,
		SharingEnabled: true,
		TimeoutMultiplier: 2.0,
		MaxRetries:     3,
		GangWaitTimeoutMin: 30,
		QueueWeights: map[string]float64{
			"high":   0.5,
			"medium": 0.3,
			"low":    0.2,
		},
		UserQuotas:     make(map[string]float64),
		TimeoutEnabled: true,
	}
}
