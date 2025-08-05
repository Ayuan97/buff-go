package task

import (
	"buff-go/internal/scraper/interfaces"
	"container/heap"
	"fmt"
	"sync"
	"time"
)

// TaskQueue 任务队列（优先级队列）
type TaskQueue []*interfaces.ScrapingTask

func (tq TaskQueue) Len() int { return len(tq) }

func (tq TaskQueue) Less(i, j int) bool {
	// 优先级高的排在前面
	return tq[i].Priority > tq[j].Priority
}

func (tq TaskQueue) Swap(i, j int) {
	tq[i], tq[j] = tq[j], tq[i]
}

func (tq *TaskQueue) Push(x interface{}) {
	*tq = append(*tq, x.(*interfaces.ScrapingTask))
}

func (tq *TaskQueue) Pop() interface{} {
	old := *tq
	n := len(old)
	item := old[n-1]
	*tq = old[0 : n-1]
	return item
}

// TaskManager 任务管理器实现
type TaskManager struct {
	queue       *TaskQueue
	queueMux    sync.Mutex
	taskStatus  map[string]interfaces.TaskStatus
	statusMux   sync.RWMutex
	taskResults map[string]*interfaces.ScrapingResult
	resultsMux  sync.RWMutex
}

// NewTaskManager 创建任务管理器
func NewTaskManager() *TaskManager {
	tm := &TaskManager{
		queue:       &TaskQueue{},
		taskStatus:  make(map[string]interfaces.TaskStatus),
		taskResults: make(map[string]*interfaces.ScrapingResult),
	}

	heap.Init(tm.queue)
	return tm
}

// AddTask 添加任务
func (tm *TaskManager) AddTask(task *interfaces.ScrapingTask) error {
	if task == nil {
		return fmt.Errorf("task cannot be nil")
	}

	if task.ID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	tm.queueMux.Lock()
	heap.Push(tm.queue, task)
	tm.queueMux.Unlock()

	// 设置任务状态
	tm.statusMux.Lock()
	tm.taskStatus[task.ID] = interfaces.TaskStatusPending
	tm.statusMux.Unlock()

	return nil
}

// GetTask 获取任务
func (tm *TaskManager) GetTask() (*interfaces.ScrapingTask, error) {
	tm.queueMux.Lock()
	defer tm.queueMux.Unlock()

	if tm.queue.Len() == 0 {
		return nil, fmt.Errorf("no tasks available")
	}

	task := heap.Pop(tm.queue).(*interfaces.ScrapingTask)

	// 更新任务状态
	tm.statusMux.Lock()
	tm.taskStatus[task.ID] = interfaces.TaskStatusRunning
	tm.statusMux.Unlock()

	return task, nil
}

// CompleteTask 完成任务
func (tm *TaskManager) CompleteTask(taskID string, result *interfaces.ScrapingResult) error {
	if taskID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	// 更新任务状态
	tm.statusMux.Lock()
	tm.taskStatus[taskID] = interfaces.TaskStatusCompleted
	tm.statusMux.Unlock()

	// 保存结果
	if result != nil {
		tm.resultsMux.Lock()
		tm.taskResults[taskID] = result
		tm.resultsMux.Unlock()
	}

	return nil
}

// FailTask 任务失败
func (tm *TaskManager) FailTask(taskID string, err error) error {
	if taskID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	// 更新任务状态
	tm.statusMux.Lock()
	tm.taskStatus[taskID] = interfaces.TaskStatusFailed
	tm.statusMux.Unlock()

	// 保存错误结果
	if err != nil {
		result := &interfaces.ScrapingResult{
			TaskID:      taskID,
			Error:       err,
			CompletedAt: time.Now(),
		}

		tm.resultsMux.Lock()
		tm.taskResults[taskID] = result
		tm.resultsMux.Unlock()
	}

	return nil
}

// GetTaskStatus 获取任务状态
func (tm *TaskManager) GetTaskStatus(taskID string) (interfaces.TaskStatus, error) {
	if taskID == "" {
		return interfaces.TaskStatusPending, fmt.Errorf("task ID cannot be empty")
	}

	tm.statusMux.RLock()
	status, exists := tm.taskStatus[taskID]
	tm.statusMux.RUnlock()

	if !exists {
		return interfaces.TaskStatusPending, fmt.Errorf("task not found: %s", taskID)
	}

	return status, nil
}

// GetQueueSize 获取队列大小
func (tm *TaskManager) GetQueueSize() int {
	tm.queueMux.Lock()
	defer tm.queueMux.Unlock()
	return tm.queue.Len()
}

// Clear 清空任务队列
func (tm *TaskManager) Clear() error {
	tm.queueMux.Lock()
	tm.queue = &TaskQueue{}
	heap.Init(tm.queue)
	tm.queueMux.Unlock()

	tm.statusMux.Lock()
	tm.taskStatus = make(map[string]interfaces.TaskStatus)
	tm.statusMux.Unlock()

	tm.resultsMux.Lock()
	tm.taskResults = make(map[string]*interfaces.ScrapingResult)
	tm.resultsMux.Unlock()

	return nil
}

// GetTaskResult 获取任务结果
func (tm *TaskManager) GetTaskResult(taskID string) (*interfaces.ScrapingResult, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task ID cannot be empty")
	}

	tm.resultsMux.RLock()
	result, exists := tm.taskResults[taskID]
	tm.resultsMux.RUnlock()

	if !exists {
		return nil, fmt.Errorf("task result not found: %s", taskID)
	}

	return result, nil
}

// GetAllTaskStatus 获取所有任务状态
func (tm *TaskManager) GetAllTaskStatus() map[string]interfaces.TaskStatus {
	tm.statusMux.RLock()
	defer tm.statusMux.RUnlock()

	result := make(map[string]interfaces.TaskStatus)
	for taskID, status := range tm.taskStatus {
		result[taskID] = status
	}

	return result
}

// RetryTask 重试任务
func (tm *TaskManager) RetryTask(taskID string) error {
	if taskID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	// 更新任务状态
	tm.statusMux.Lock()
	if _, exists := tm.taskStatus[taskID]; !exists {
		tm.statusMux.Unlock()
		return fmt.Errorf("task not found: %s", taskID)
	}
	tm.taskStatus[taskID] = interfaces.TaskStatusRetrying
	tm.statusMux.Unlock()

	return nil
}

// CancelTask 取消任务
func (tm *TaskManager) CancelTask(taskID string) error {
	if taskID == "" {
		return fmt.Errorf("task ID cannot be empty")
	}

	// 更新任务状态
	tm.statusMux.Lock()
	if _, exists := tm.taskStatus[taskID]; !exists {
		tm.statusMux.Unlock()
		return fmt.Errorf("task not found: %s", taskID)
	}
	tm.taskStatus[taskID] = interfaces.TaskStatusCancelled
	tm.statusMux.Unlock()

	return nil
}

// GetStats 获取任务管理器统计信息
func (tm *TaskManager) GetStats() map[string]int {
	tm.statusMux.RLock()
	defer tm.statusMux.RUnlock()

	stats := map[string]int{
		"pending":   0,
		"running":   0,
		"completed": 0,
		"failed":    0,
		"retrying":  0,
		"cancelled": 0,
	}

	for _, status := range tm.taskStatus {
		switch status {
		case interfaces.TaskStatusPending:
			stats["pending"]++
		case interfaces.TaskStatusRunning:
			stats["running"]++
		case interfaces.TaskStatusCompleted:
			stats["completed"]++
		case interfaces.TaskStatusFailed:
			stats["failed"]++
		case interfaces.TaskStatusRetrying:
			stats["retrying"]++
		case interfaces.TaskStatusCancelled:
			stats["cancelled"]++
		}
	}

	stats["queue_size"] = tm.GetQueueSize()
	return stats
}
