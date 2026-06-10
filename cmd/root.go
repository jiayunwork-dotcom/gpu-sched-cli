package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gpu-sched-cli/internal/lifecycle"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/queue"
	"github.com/gpu-sched-cli/internal/scheduler"
	"github.com/gpu-sched-cli/internal/store"
)

var (
	globalStore     *store.Store
	globalQueue     *queue.PriorityQueue
	globalScheduler *scheduler.Scheduler
	globalLifecycle *lifecycle.Manager
	globalFairMgr   *queue.FairShareManager
	globalBorrowMgr *queue.BorrowManager
	stateMgr        *store.StateManager
	clusterFile     string
	schedulerFile   string
)

func initGlobals() error {
	clusterCfg, err := loadClusterConfig(clusterFile)
	if err != nil {
		return fmt.Errorf("load cluster config: %w", err)
	}
	cluster := model.NewCluster(clusterCfg)

	schedCfg, err := loadSchedulerConfig(schedulerFile)
	if err != nil {
		schedCfg = model.DefaultSchedulerConfig()
	}

	globalStore = store.NewStore(cluster, schedCfg)
	globalQueue = queue.NewPriorityQueue(globalStore)
	globalFairMgr = queue.NewFairShareManager(globalStore)
	globalBorrowMgr = queue.NewBorrowManager(globalStore, globalQueue)
	globalScheduler = scheduler.NewScheduler(globalStore, globalQueue, globalFairMgr, globalBorrowMgr)
	globalLifecycle = lifecycle.NewManager(globalStore, globalQueue, globalScheduler)
	stateMgr = store.NewStateManager(globalStore)

	if err := stateMgr.Load(); err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	requeuePendingTasks()

	return nil
}

func requeuePendingTasks() {
	tasks := globalStore.GetAllTasks()
	for _, t := range tasks {
		if t.Status == model.TaskStatusQueued || t.Status == model.TaskStatusSubmitted {
			globalQueue.Enqueue(t)
		}
		if t.Status == model.TaskStatusPreempted {
			globalStore.RequeuePreemptedTask(t.ID)
			globalQueue.RequeueFront(t)
		}
	}
}

func initSchedulerBackground() {
	globalScheduler.Start()
	globalLifecycle.Start()
}

func saveState() {
	if stateMgr != nil {
		_ = stateMgr.Save()
	}
}

func startServe() {
	initSchedulerBackground()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			saveState()
		case sig := <-sigCh:
			fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
			saveState()
			globalScheduler.Stop()
			globalLifecycle.Stop()
			return
		}
	}
}

func loadClusterConfig(path string) (*model.ClusterConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg model.ClusterConfig
	if err := yamlUnmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func loadSchedulerConfig(path string) (*model.SchedulerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg model.SchedulerConfig
	if err := yamlUnmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
