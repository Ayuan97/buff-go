package framework

import (
	"container/heap"
	"fmt"
	"sync"
	"time"
)

// TaskQueue 任务队列（优先级队列）
type TaskQueue []*ScrapingTask

func (tq TaskQueue) Len() int { return len(tq) }

func (tq TaskQueue) Less(i, j int) bool {
	// 优先级高的排在前面
	return tq[i].Priority > tq[j].Priority
}

func (tq TaskQueue) Swap(i, j int) {
	tq[i], tq[j] = tq[j], tq[i]
}

func (tq *TaskQueue) Push(x interface{}) {
	*tq = append(*tq, x.(*ScrapingTask))
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
	queue        *TaskQueue
	queueMux     sync.Mutex
	tasks        map[string]*TaskInfo
	tasksMux     sync.RWMutex
	maxQueueSize int
}

// TaskInfo 任务信息
type TaskInfo struct {
	Task      *ScrapingTask   `json:"task"`
	Status    TaskStatus      `json:"status"`
	Result    *ScrapingResult `json:"result"`
	Error     error           `json:"error"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// NewTaskManager 创建任务管理器
func NewTaskManager(maxQueueSize int) *TaskManager {
	if maxQueueSize <= 0 {
		maxQueueSize = 10000
	}

	queue := make(TaskQueue, 0)
	heap.Init(&queue)

	return &TaskManager{
		queue:        &queue,
		tasks:        make(map[string]*TaskInfo),
		maxQueueSize: maxQueueSize,
	}
}

// AddTask 添加任务
func (tm *TaskManager) AddTask(task *ScrapingTask) error {
	if task == nil {
		return fmt.Errorf("task cannot be nil")
	}

	if task.ID == "" {
		task.ID = tm.generateTaskID()
	}

	tm.queueMux.Lock()
	defer tm.queueMux.Unlock()

	// 检查队列大小
	if tm.queue.Len() >= tm.maxQueueSize {
		return fmt.Errorf("task queue is full")
	}

	// 添加到队列
	heap.Push(tm.queue, task)

	// 记录任务信息
	taskInfo := &TaskInfo{
		Task:      task,
		Status:    TaskStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	tm.tasksMux.Lock()
	tm.tasks[task.ID] = taskInfo
	tm.tasksMux.Unlock()

	return nil
}

// GetTask 获取任务
func (tm *TaskManager) GetTask() (*ScrapingTask, error) {
	tm.queueMux.Lock()
	defer tm.queueMux.Unlock()

	if tm.queue.Len() == 0 {
		return nil, fmt.Errorf("no tasks available")
	}

	task := heap.Pop(tm.queue).(*ScrapingTask)

	// 更新任务状态
	tm.tasksMux.Lock()
	if taskInfo, exists := tm.tasks[task.ID]; exists {
		taskInfo.Status = TaskStatusRunning
		taskInfo.UpdatedAt = time.Now()
	}
	tm.tasksMux.Unlock()

	return task, nil
}

// CompleteTask 完成任务
func (tm *TaskManager) CompleteTask(taskID string, result *ScrapingResult) error {
	tm.tasksMux.Lock()
	defer tm.tasksMux.Unlock()

	taskInfo, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	taskInfo.Status = TaskStatusCompleted
	taskInfo.Result = result
	taskInfo.UpdatedAt = time.Now()

	return nil
}

// FailTask 任务失败
func (tm *TaskManager) FailTask(taskID string, err error) error {
	tm.tasksMux.Lock()
	defer tm.tasksMux.Unlock()

	taskInfo, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	// 检查是否需要重试
	if taskInfo.Task.RetryCount > 0 {
		taskInfo.Task.RetryCount--
		taskInfo.Status = TaskStatusRetrying
		taskInfo.Error = err
		taskInfo.UpdatedAt = time.Now()

		// 重新添加到队列
		tm.queueMux.Lock()
		heap.Push(tm.queue, taskInfo.Task)
		tm.queueMux.Unlock()

		return nil
	}

	taskInfo.Status = TaskStatusFailed
	taskInfo.Error = err
	taskInfo.UpdatedAt = time.Now()

	return nil
}

// GetTaskStatus 获取任务状态
func (tm *TaskManager) GetTaskStatus(taskID string) (TaskStatus, error) {
	tm.tasksMux.RLock()
	defer tm.tasksMux.RUnlock()

	taskInfo, exists := tm.tasks[taskID]
	if !exists {
		return TaskStatusPending, fmt.Errorf("task not found: %s", taskID)
	}

	return taskInfo.Status, nil
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
	tm.tasksMux.Lock()
	defer tm.queueMux.Unlock()
	defer tm.tasksMux.Unlock()

	// 清空队列
	*tm.queue = (*tm.queue)[:0]
	heap.Init(tm.queue)

	// 清空任务记录
	tm.tasks = make(map[string]*TaskInfo)

	return nil
}

// GetTaskInfo 获取任务信息
func (tm *TaskManager) GetTaskInfo(taskID string) (*TaskInfo, error) {
	tm.tasksMux.RLock()
	defer tm.tasksMux.RUnlock()

	taskInfo, exists := tm.tasks[taskID]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// 返回副本
	return &TaskInfo{
		Task:      taskInfo.Task,
		Status:    taskInfo.Status,
		Result:    taskInfo.Result,
		Error:     taskInfo.Error,
		CreatedAt: taskInfo.CreatedAt,
		UpdatedAt: taskInfo.UpdatedAt,
	}, nil
}

// GetAllTasks 获取所有任务信息
func (tm *TaskManager) GetAllTasks() map[string]*TaskInfo {
	tm.tasksMux.RLock()
	defer tm.tasksMux.RUnlock()

	result := make(map[string]*TaskInfo)
	for id, taskInfo := range tm.tasks {
		result[id] = &TaskInfo{
			Task:      taskInfo.Task,
			Status:    taskInfo.Status,
			Result:    taskInfo.Result,
			Error:     taskInfo.Error,
			CreatedAt: taskInfo.CreatedAt,
			UpdatedAt: taskInfo.UpdatedAt,
		}
	}

	return result
}

// GetTasksByStatus 根据状态获取任务
func (tm *TaskManager) GetTasksByStatus(status TaskStatus) []*TaskInfo {
	tm.tasksMux.RLock()
	defer tm.tasksMux.RUnlock()

	var result []*TaskInfo
	for _, taskInfo := range tm.tasks {
		if taskInfo.Status == status {
			result = append(result, &TaskInfo{
				Task:      taskInfo.Task,
				Status:    taskInfo.Status,
				Result:    taskInfo.Result,
				Error:     taskInfo.Error,
				CreatedAt: taskInfo.CreatedAt,
				UpdatedAt: taskInfo.UpdatedAt,
			})
		}
	}

	return result
}

// CancelTask 取消任务
func (tm *TaskManager) CancelTask(taskID string) error {
	tm.tasksMux.Lock()
	defer tm.tasksMux.Unlock()

	taskInfo, exists := tm.tasks[taskID]
	if !exists {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if taskInfo.Status == TaskStatusRunning {
		return fmt.Errorf("cannot cancel running task")
	}

	taskInfo.Status = TaskStatusCancelled
	taskInfo.UpdatedAt = time.Now()

	return nil
}

// CleanupCompletedTasks 清理已完成的任务
func (tm *TaskManager) CleanupCompletedTasks(olderThan time.Duration) int {
	tm.tasksMux.Lock()
	defer tm.tasksMux.Unlock()

	cutoff := time.Now().Add(-olderThan)
	cleaned := 0

	for id, taskInfo := range tm.tasks {
		if (taskInfo.Status == TaskStatusCompleted || taskInfo.Status == TaskStatusFailed) &&
			taskInfo.UpdatedAt.Before(cutoff) {
			delete(tm.tasks, id)
			cleaned++
		}
	}

	return cleaned
}

// generateTaskID 生成任务ID
func (tm *TaskManager) generateTaskID() string {
	return fmt.Sprintf("task_%d", time.Now().UnixNano())
}
