package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/navidrome/navidrome/log"
	"github.com/navidrome/navidrome/model/id"
	"github.com/navidrome/navidrome/plugins/capabilities"
	"github.com/navidrome/navidrome/plugins/host"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/time/rate"
)

const (
	defaultConcurrency  int32 = 1
	defaultBackoffMs    int64 = 1000
	defaultRetentionMs  int64 = 3_600_000
	minRetentionMs      int64 = 60_000
	maxRetentionMs      int64 = 604_800_000
	maxQueueNameLength        = 128
	maxPayloadSize            = 1 * 1024 * 1024
	maxBackoffMs        int64 = 3_600_000
	taskCleanupInterval       = 5 * time.Minute
	pollInterval              = 5 * time.Second
	shutdownTimeout           = 10 * time.Second

	taskStatusPending   = "pending"
	taskStatusRunning   = "running"
	taskStatusCompleted = "completed"
	taskStatusFailed    = "failed"
	taskStatusCancelled = "cancelled"
)

const CapabilityTaskWorker Capability = "TaskWorker"
const FuncTaskWorkerCallback = "nd_task_execute"

func init() {
	registerCapability(CapabilityTaskWorker, FuncTaskWorkerCallback)
}

type queueState struct {
	config  host.QueueConfig
	signal  chan struct{}
	limiter *rate.Limiter
}

func (qs *queueState) notifyWorkers() {
	select {
	case qs.signal <- struct{}{}:
	default:
	}
}

type taskQueueServiceImpl struct {
	pluginName     string
	manager        *Manager
	maxConcurrency int32
	queuesCol      *mongo.Collection
	tasksCol       *mongo.Collection
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	mu             sync.Mutex
	queues         map[string]*queueState

	invokeCallbackFn func(ctx context.Context, queueName, taskID string, payload []byte, attempt int32) (string, error)
}

type queueRecord struct {
	Plugin string           `bson:"plugin"`
	Name   string           `bson:"name"`
	Config host.QueueConfig `bson:"config"`
}

type taskRecord struct {
	ID         string `bson:"id"`
	Plugin     string `bson:"plugin"`
	QueueName  string `bson:"queueName"`
	Payload    []byte `bson:"payload"`
	Status     string `bson:"status"`
	Attempt    int32  `bson:"attempt"`
	MaxRetries int32  `bson:"maxRetries"`
	NextRunAt  int64  `bson:"nextRunAt"`
	CreatedAt  int64  `bson:"createdAt"`
	UpdatedAt  int64  `bson:"updatedAt"`
	Message    string `bson:"message"`
}

func newTaskQueueService(pluginName string, manager *Manager, maxConcurrency int32) (*taskQueueServiceImpl, error) {
	db, err := pluginMongoDB(manager.ctx)
	if err != nil {
		return nil, fmt.Errorf("connecting plugin taskqueue to MongoDB: %w", err)
	}
	ctx, cancel := context.WithCancel(manager.ctx)
	s := &taskQueueServiceImpl{
		pluginName:     pluginName,
		manager:        manager,
		maxConcurrency: maxConcurrency,
		queuesCol:      db.Collection("plugin_queues"),
		tasksCol:       db.Collection("plugin_tasks"),
		ctx:            ctx,
		cancel:         cancel,
		queues:         map[string]*queueState{},
	}
	if err := s.ensureIndexes(ctx); err != nil {
		cancel()
		return nil, err
	}
	s.invokeCallbackFn = s.defaultInvokeCallback
	s.wg.Go(s.cleanupLoop)
	log.Debug(ctx, "Initialized plugin taskqueue", "plugin", pluginName, "backend", "mongo", "maxConcurrency", maxConcurrency)
	return s, nil
}

func (s *taskQueueServiceImpl) ensureIndexes(ctx context.Context) error {
	_, err := s.queuesCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "plugin", Value: 1}, {Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)},
	})
	if err != nil {
		return err
	}
	_, err = s.tasksCol.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "plugin", Value: 1}, {Key: "queueName", Value: 1}, {Key: "status", Value: 1}, {Key: "nextRunAt", Value: 1}}},
	})
	return err
}

func (s *taskQueueServiceImpl) applyConfigDefaults(ctx context.Context, name string, config *host.QueueConfig) {
	if config.Concurrency <= 0 {
		config.Concurrency = defaultConcurrency
	}
	if config.BackoffMs <= 0 {
		config.BackoffMs = defaultBackoffMs
	}
	if config.RetentionMs <= 0 {
		config.RetentionMs = defaultRetentionMs
	}
	if config.RetentionMs < minRetentionMs {
		log.Warn(ctx, "TaskQueue retention clamped to minimum", "plugin", s.pluginName, "queue", name, "requested", config.RetentionMs)
		config.RetentionMs = minRetentionMs
	}
	if config.RetentionMs > maxRetentionMs {
		log.Warn(ctx, "TaskQueue retention clamped to maximum", "plugin", s.pluginName, "queue", name, "requested", config.RetentionMs)
		config.RetentionMs = maxRetentionMs
	}
}

func (s *taskQueueServiceImpl) clampConcurrency(ctx context.Context, name string, config *host.QueueConfig) error {
	var allocated int32
	for _, qs := range s.queues {
		allocated += qs.config.Concurrency
	}
	available := s.maxConcurrency - allocated
	if available <= 0 {
		return fmt.Errorf("concurrency budget exhausted (%d/%d allocated)", allocated, s.maxConcurrency)
	}
	if config.Concurrency > available {
		log.Warn(ctx, "TaskQueue concurrency clamped", "plugin", s.pluginName, "queue", name, "requested", config.Concurrency, "available", available)
		config.Concurrency = available
	}
	return nil
}

func (s *taskQueueServiceImpl) CreateQueue(ctx context.Context, name string, config host.QueueConfig) error {
	if name == "" {
		return fmt.Errorf("queue name cannot be empty")
	}
	if len(name) > maxQueueNameLength {
		return fmt.Errorf("queue name exceeds maximum length of %d bytes", maxQueueNameLength)
	}
	s.applyConfigDefaults(ctx, name, &config)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.queues[name]; exists {
		return fmt.Errorf("queue %q already exists", name)
	}
	if err := s.clampConcurrency(ctx, name, &config); err != nil {
		return err
	}
	_, err := s.queuesCol.UpdateOne(ctx, bson.M{"plugin": s.pluginName, "name": name}, bson.M{"$set": queueRecord{Plugin: s.pluginName, Name: name, Config: config}}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	if _, err := s.tasksCol.UpdateMany(ctx, bson.M{"plugin": s.pluginName, "queueName": name, "status": taskStatusRunning}, bson.M{"$set": bson.M{"status": taskStatusPending, "updatedAt": now}}); err != nil {
		return err
	}
	qs := &queueState{config: config, signal: make(chan struct{}, 1)}
	if config.DelayMs > 0 {
		qs.limiter = rate.NewLimiter(rate.Every(time.Duration(config.DelayMs)*time.Millisecond), 1)
	}
	s.queues[name] = qs
	for i := int32(0); i < config.Concurrency; i++ {
		s.wg.Go(func() { s.worker(name, qs) })
	}
	return nil
}

func (s *taskQueueServiceImpl) Enqueue(ctx context.Context, queueName string, payload []byte) (string, error) {
	s.mu.Lock()
	qs, exists := s.queues[queueName]
	s.mu.Unlock()
	if !exists {
		return "", fmt.Errorf("queue %q does not exist", queueName)
	}
	if len(payload) > maxPayloadSize {
		return "", fmt.Errorf("payload size %d exceeds maximum of %d bytes", len(payload), maxPayloadSize)
	}
	taskID := id.NewRandom()
	now := time.Now().UnixMilli()
	_, err := s.tasksCol.InsertOne(ctx, taskRecord{ID: taskID, Plugin: s.pluginName, QueueName: queueName, Payload: payload, Status: taskStatusPending, MaxRetries: qs.config.MaxRetries, NextRunAt: now, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return "", err
	}
	qs.notifyWorkers()
	return taskID, nil
}

func (s *taskQueueServiceImpl) Get(ctx context.Context, taskID string) (*host.TaskInfo, error) {
	var rec taskRecord
	err := s.tasksCol.FindOne(ctx, bson.M{"plugin": s.pluginName, "id": taskID}).Decode(&rec)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, fmt.Errorf("task %q not found", taskID)
	}
	if err != nil {
		return nil, err
	}
	return &host.TaskInfo{Status: rec.Status, Message: rec.Message, Attempt: rec.Attempt}, nil
}

func (s *taskQueueServiceImpl) Cancel(ctx context.Context, taskID string) error {
	now := time.Now().UnixMilli()
	res, err := s.tasksCol.UpdateOne(ctx, bson.M{"plugin": s.pluginName, "id": taskID, "status": taskStatusPending}, bson.M{"$set": bson.M{"status": taskStatusCancelled, "updatedAt": now}})
	if err != nil {
		return err
	}
	if res.ModifiedCount == 0 {
		return fmt.Errorf("task %q cannot be cancelled", taskID)
	}
	return nil
}

func (s *taskQueueServiceImpl) ClearQueue(ctx context.Context, queueName string) (int64, error) {
	s.mu.Lock()
	_, exists := s.queues[queueName]
	s.mu.Unlock()
	if !exists {
		return 0, fmt.Errorf("queue %q does not exist", queueName)
	}
	now := time.Now().UnixMilli()
	res, err := s.tasksCol.UpdateMany(ctx, bson.M{"plugin": s.pluginName, "queueName": queueName, "status": taskStatusPending}, bson.M{"$set": bson.M{"status": taskStatusCancelled, "updatedAt": now}})
	if err != nil {
		return 0, err
	}
	return res.ModifiedCount, nil
}

func (s *taskQueueServiceImpl) worker(queueName string, qs *queueState) {
	s.drainQueue(queueName, qs)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-qs.signal:
			s.drainQueue(queueName, qs)
		case <-ticker.C:
			s.drainQueue(queueName, qs)
		}
	}
}

func (s *taskQueueServiceImpl) drainQueue(queueName string, qs *queueState) {
	for s.ctx.Err() == nil && s.processTask(queueName, qs) {
	}
}

func (s *taskQueueServiceImpl) processTask(queueName string, qs *queueState) bool {
	now := time.Now().UnixMilli()
	var rec taskRecord
	err := s.tasksCol.FindOneAndUpdate(
		s.ctx,
		bson.M{"plugin": s.pluginName, "queueName": queueName, "status": taskStatusPending, "nextRunAt": bson.M{"$lte": now}},
		bson.M{"$set": bson.M{"status": taskStatusRunning, "updatedAt": now}, "$inc": bson.M{"attempt": 1}},
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "nextRunAt", Value: 1}, {Key: "createdAt", Value: 1}}).SetReturnDocument(options.After),
	).Decode(&rec)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false
	}
	if err != nil {
		log.Error(s.ctx, "Failed to dequeue task", "plugin", s.pluginName, "queue", queueName, err)
		return false
	}
	if qs.limiter != nil {
		if err := qs.limiter.Wait(s.ctx); err != nil {
			s.revertTaskToPending(rec.ID)
			return false
		}
	}
	message, callbackErr := s.invokeCallbackFn(s.ctx, queueName, rec.ID, rec.Payload, rec.Attempt)
	if s.ctx.Err() != nil {
		s.revertTaskToPending(rec.ID)
		return false
	}
	if callbackErr == nil {
		s.completeTask(rec.ID, message)
	} else {
		s.handleTaskFailure(rec.ID, rec.Attempt, rec.MaxRetries, qs, callbackErr, message)
	}
	return true
}

func (s *taskQueueServiceImpl) completeTask(taskID, message string) {
	now := time.Now().UnixMilli()
	_, err := s.tasksCol.UpdateOne(s.ctx, bson.M{"plugin": s.pluginName, "id": taskID}, bson.M{"$set": bson.M{"status": taskStatusCompleted, "message": message, "updatedAt": now}})
	if err != nil {
		log.Error(s.ctx, "Failed to mark task completed", "plugin", s.pluginName, "taskID", taskID, err)
	}
}

func (s *taskQueueServiceImpl) handleTaskFailure(taskID string, attempt, maxRetries int32, qs *queueState, callbackErr error, message string) {
	if message == "" {
		message = callbackErr.Error()
	}
	now := time.Now().UnixMilli()
	if attempt > maxRetries {
		_, _ = s.tasksCol.UpdateOne(s.ctx, bson.M{"plugin": s.pluginName, "id": taskID}, bson.M{"$set": bson.M{"status": taskStatusFailed, "message": message, "updatedAt": now}})
		return
	}
	backoff := qs.config.BackoffMs << (attempt - 1)
	if backoff <= 0 || backoff > maxBackoffMs {
		backoff = maxBackoffMs
	}
	_, _ = s.tasksCol.UpdateOne(s.ctx, bson.M{"plugin": s.pluginName, "id": taskID}, bson.M{"$set": bson.M{"status": taskStatusPending, "message": message, "nextRunAt": now + backoff, "updatedAt": now}})
	time.AfterFunc(time.Duration(backoff)*time.Millisecond, qs.notifyWorkers)
}

func (s *taskQueueServiceImpl) revertTaskToPending(taskID string) {
	now := time.Now().UnixMilli()
	_, _ = s.tasksCol.UpdateOne(s.ctx, bson.M{"plugin": s.pluginName, "id": taskID, "status": taskStatusRunning}, bson.M{"$set": bson.M{"status": taskStatusPending, "updatedAt": now}, "$inc": bson.M{"attempt": -1}})
}

func (s *taskQueueServiceImpl) defaultInvokeCallback(ctx context.Context, queueName, taskID string, payload []byte, attempt int32) (string, error) {
	s.manager.mu.RLock()
	p, ok := s.manager.plugins[s.pluginName]
	s.manager.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("plugin %s not loaded", s.pluginName)
	}
	return callPluginFunction[capabilities.TaskExecuteRequest, string](ctx, p, FuncTaskWorkerCallback, capabilities.TaskExecuteRequest{QueueName: queueName, TaskID: taskID, Payload: payload, Attempt: attempt})
}

func (s *taskQueueServiceImpl) cleanupLoop() {
	ticker := time.NewTicker(taskCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.runCleanup()
		}
	}
}

func (s *taskQueueServiceImpl) runCleanup() {
	s.mu.Lock()
	queues := make(map[string]*queueState, len(s.queues))
	for k, v := range s.queues {
		queues[k] = v
	}
	s.mu.Unlock()
	now := time.Now().UnixMilli()
	for name, qs := range queues {
		_, _ = s.tasksCol.DeleteMany(s.ctx, bson.M{"plugin": s.pluginName, "queueName": name, "status": bson.M{"$in": []string{taskStatusCompleted, taskStatusFailed, taskStatusCancelled}}, "updatedAt": bson.M{"$lt": now - qs.config.RetentionMs}})
	}
}

func (s *taskQueueServiceImpl) Close() error {
	s.cancel()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownTimeout):
		log.Warn("TaskQueue shutdown timed out", "plugin", s.pluginName)
	}
	now := time.Now().UnixMilli()
	_, _ = s.tasksCol.UpdateMany(context.Background(), bson.M{"plugin": s.pluginName, "status": taskStatusRunning}, bson.M{"$set": bson.M{"status": taskStatusPending, "updatedAt": now}})
	return nil
}

var _ host.TaskService = (*taskQueueServiceImpl)(nil)
var _ io.Closer = (*taskQueueServiceImpl)(nil)
