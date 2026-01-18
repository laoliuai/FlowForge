package scheduler

import (
	"sort"

	"github.com/flowforge/flowforge/pkg/model"
)

type FairScheduler struct{}

func NewFairScheduler() *FairScheduler {
	return &FairScheduler{}
}

func (f *FairScheduler) SortByPriority(tasks []model.Task) {
	sort.Slice(tasks, func(i, j int) bool {
		return taskPriority(tasks[i]) > taskPriority(tasks[j])
	})
}

func taskPriority(task model.Task) int {
	if task.RetryCount > 0 {
		return 1000 - task.RetryCount
	}
	return 0
}
