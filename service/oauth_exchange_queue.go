package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/oauthqueue"
)

type OAuthExchangeTicket struct {
	ExchangeID       string
	PollToken        string
	Status           oauthqueue.Status
	ExpiresIn        int
	Created          bool
	EnqueueConfirmed bool
}

type OAuthExchangeResult struct {
	Status                oauthqueue.Status `json:"status"`
	AccessToken           string            `json:"access_token,omitempty"`
	TokenType             string            `json:"token_type,omitempty"`
	ExpiresIn             int               `json:"expires_in,omitempty"`
	Scope                 string            `json:"scope,omitempty"`
	RefreshToken          string            `json:"refresh_token,omitempty"`
	RefreshTokenExpiresIn int               `json:"refresh_token_expires_in,omitempty"`
	Error                 string            `json:"error,omitempty"`
	ErrorDescription      string            `json:"error_description,omitempty"`
	RetryAfter            int               `json:"retry_after,omitempty"`
	Reauthorize           bool              `json:"reauthorize,omitempty"`
}

type queuedOAuthExchangePayload struct {
	Code                string `json:"code"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	AppID               int    `json:"app_id"`
	AppName             string `json:"app_name"`
	AppClientType       string `json:"app_client_type"`
	UserID              int    `json:"user_id"`
	RequestID           string `json:"request_id"`
	ClientIP            string `json:"client_ip"`
	UserAgent           string `json:"user_agent"`
	DeadlineUnixMilli   int64  `json:"deadline_unix_milli"`
}

type queuedOAuthExchangeResult struct {
	AccessToken           string `json:"access_token"`
	ExpiresIn             int    `json:"expires_in"`
	Scope                 string `json:"scope"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
}

type oauthQueueRuntime struct {
	queue           *oauthqueue.Queue
	clients         *oauthqueue.ClientSet
	stop            chan struct{}
	stopping        atomic.Bool
	wg              sync.WaitGroup
	owner           string
	validationSlots chan struct{}
	validationWait  atomic.Int64
	errorLogAt      atomic.Int64
}

type OAuthExchangeAdmission struct {
	runtime      *oauthQueueRuntime
	permit       *oauthqueue.Permit
	startedAt    time.Time
	dbWaitBefore int64
	renewDone    chan struct{}
	finished     atomic.Bool
}

type OAuthValidationAdmission struct {
	runtime  *oauthQueueRuntime
	exchange *OAuthExchangeAdmission
	finished atomic.Bool
}

var (
	oauthQueueMu      sync.RWMutex
	oauthQueueCurrent *oauthQueueRuntime
)

const (
	OAuthUserOperationToken      = "token"
	OAuthUserOperationRefresh    = "refresh"
	OAuthUserOperationUserInfo   = "userinfo"
	oauthQueuedClientUnavailable = "client is disabled or deleted"
	oauthQueueMaintenanceBatch   = 100
	oauthQueueCleanupMaxBatches  = 10
)

func StartOAuthExchangeQueue() error {
	config, err := oauthqueue.LoadConfigFromEnv()
	if err != nil {
		return err
	}
	if !config.Enabled {
		common.SysLog("OAuth exchange queue is disabled")
		return nil
	}
	if common.RDB == nil && len(config.QueueRedisURLs) == 0 && len(config.ClusterAddrs) == 0 && len(config.SentinelAddrs) == 0 {
		if enabled, configured := os.LookupEnv("OAUTH_QUEUE_ENABLE"); !configured || strings.TrimSpace(enabled) == "" {
			common.SysLog("OAuth exchange queue is disabled because Redis is not configured")
			return nil
		}
	}
	clients, err := oauthqueue.NewClientSet(context.Background(), config, common.RDB)
	if err != nil {
		return err
	}
	queue, err := oauthqueue.New(config, clients)
	if err != nil {
		clients.Close()
		return err
	}
	runtime := &oauthQueueRuntime{
		queue:           queue,
		clients:         clients,
		stop:            make(chan struct{}),
		owner:           common.GetUUID(),
		validationSlots: make(chan struct{}, config.WorkersPerInstance),
	}
	oauthQueueMu.Lock()
	previous := oauthQueueCurrent
	oauthQueueCurrent = runtime
	oauthQueueMu.Unlock()
	if previous != nil {
		stopOAuthQueueRuntime(context.Background(), previous)
	}
	for worker := 0; worker < config.WorkersPerInstance; worker++ {
		runtime.wg.Add(1)
		go runtime.workerLoop(worker)
	}
	runtime.wg.Add(1)
	go runtime.maintenanceLoop()
	runtime.wg.Add(1)
	go runtime.adaptiveLoop()
	common.SysLog(fmt.Sprintf(
		"OAuth exchange queue started: partitions=%d capacity=%d workers=%d concurrency=%d..%d",
		config.Partitions,
		config.Capacity,
		config.WorkersPerInstance,
		config.MinConcurrency,
		config.MaxConcurrency,
	))
	return nil
}

func StopOAuthExchangeQueue(ctx context.Context) error {
	oauthQueueMu.Lock()
	runtime := oauthQueueCurrent
	oauthQueueCurrent = nil
	oauthQueueMu.Unlock()
	if runtime == nil {
		return nil
	}
	return stopOAuthQueueRuntime(ctx, runtime)
}

func stopOAuthQueueRuntime(ctx context.Context, runtime *oauthQueueRuntime) error {
	if runtime.stopping.CompareAndSwap(false, true) {
		close(runtime.stop)
	}
	done := make(chan struct{})
	go func() {
		runtime.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		runtime.clients.Close()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func OAuthExchangeQueueEnabled() bool {
	return currentOAuthQueue() != nil
}

func OAuthExchangeQueueConfig() (oauthqueue.Config, bool) {
	runtime := currentOAuthQueue()
	if runtime == nil {
		return oauthqueue.Config{}, false
	}
	return runtime.queue.Config(), true
}

func OAuthExchangeRequestContext(parent context.Context) (context.Context, context.CancelFunc) {
	runtime := currentOAuthQueue()
	if runtime == nil {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, runtime.queue.Config().JobTTL)
}

func AllowOAuthExchangeUser(ctx context.Context, operation string, appID int, userID int) (oauthqueue.UserDecision, error) {
	runtime := currentOAuthQueue()
	if runtime == nil {
		return oauthqueue.UserDecision{Allowed: true}, nil
	}
	return runtime.queue.AllowUserOperation(ctx, operation, appID, userID)
}

func RefundOAuthExchangeUser(ctx context.Context, operation string, appID int, userID int) error {
	runtime := currentOAuthQueue()
	if runtime == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	refundCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return runtime.queue.RefundUserOperation(refundCtx, operation, appID, userID)
}

func EnqueueOAuthAuthorizationCode(
	ctx context.Context,
	app *model.OAuthApp,
	authorizationCode *model.OAuthAuthorizationCode,
	requestID string,
	clientIP string,
	userAgent string,
) (*OAuthExchangeTicket, error) {
	runtime := currentOAuthQueue()
	if runtime == nil || runtime.stopping.Load() {
		return nil, fmt.Errorf("OAuth exchange queue is unavailable")
	}
	if app == nil || authorizationCode == nil {
		return nil, fmt.Errorf("OAuth exchange facts are incomplete")
	}
	config := runtime.queue.Config()
	exchangeID := common.GenerateHMAC("oauth-exchange:" + app.ClientId + ":" + authorizationCode.Code)
	pollToken := common.GenerateHMAC("oauth-poll:" + app.ClientId + ":" + authorizationCode.Code)
	deadline := time.Now().Add(config.JobTTL)
	if authorizationCode.ExpiresAt.Before(deadline) {
		deadline = authorizationCode.ExpiresAt
	}
	ticket := &OAuthExchangeTicket{
		ExchangeID: exchangeID,
		PollToken:  pollToken,
		Status:     oauthqueue.StatusPending,
		ExpiresIn:  int(time.Until(deadline).Seconds()),
	}
	payload, err := encryptOAuthQueueValue(queuedOAuthExchangePayload{
		Code:                authorizationCode.Code,
		ClientID:            app.ClientId,
		RedirectURI:         authorizationCode.RedirectUri,
		CodeChallenge:       authorizationCode.CodeChallenge,
		CodeChallengeMethod: authorizationCode.CodeChallengeMethod,
		AppID:               app.Id,
		AppName:             app.Name,
		AppClientType:       app.EffectiveClientType(),
		UserID:              authorizationCode.UserId,
		RequestID:           requestID,
		ClientIP:            clientIP,
		UserAgent:           userAgent,
		DeadlineUnixMilli:   deadline.UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	created, err := runtime.queue.Enqueue(ctx, oauthqueue.EnqueueInput{
		ID:        exchangeID,
		Payload:   payload,
		PollHash:  common.GenerateHMAC("oauth-poll-hash:" + pollToken),
		Deadline:  deadline,
		CleanupAt: time.Now().Add(config.ResultTTL),
	})
	if err != nil {
		if errors.Is(err, oauthqueue.ErrQueueFull) {
			return nil, err
		}
		return ticket, err
	}
	ticket.Created = created
	ticket.EnqueueConfirmed = true
	snapshot, err := runtime.queue.Snapshot(ctx, exchangeID, common.GenerateHMAC("oauth-poll-hash:"+pollToken))
	if err != nil {
		return ticket, err
	}
	ticket.Status = snapshot.Status
	return ticket, nil
}

func WaitOAuthExchangeResult(ctx context.Context, exchangeID string, pollToken string, wait time.Duration) (*OAuthExchangeResult, error) {
	deadline := time.Now().Add(wait)
	for {
		result, err := GetOAuthExchangeResult(ctx, exchangeID, pollToken)
		if err != nil {
			return nil, err
		}
		switch result.Status {
		case oauthqueue.StatusSucceeded, oauthqueue.StatusFailed, oauthqueue.StatusUnknown, oauthqueue.StatusExpired:
			return result, nil
		}
		if wait <= 0 || !time.Now().Before(deadline) {
			return result, nil
		}
		runtime := currentOAuthQueue()
		if runtime == nil {
			return nil, fmt.Errorf("OAuth exchange queue is unavailable")
		}
		timer := time.NewTimer(runtime.queue.Config().PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func GetOAuthExchangeResult(ctx context.Context, exchangeID string, pollToken string) (*OAuthExchangeResult, error) {
	runtime := currentOAuthQueue()
	if runtime == nil {
		return nil, fmt.Errorf("OAuth exchange queue is unavailable")
	}
	snapshot, err := runtime.queue.Snapshot(
		ctx,
		exchangeID,
		common.GenerateHMAC("oauth-poll-hash:"+pollToken),
	)
	if err != nil {
		return nil, err
	}
	result := &OAuthExchangeResult{
		Status:     snapshot.Status,
		RetryAfter: int(runtime.queue.Config().PollInterval/time.Second) + 1,
	}
	switch snapshot.Status {
	case oauthqueue.StatusSucceeded:
		var stored queuedOAuthExchangeResult
		if err := decryptOAuthQueueValue(snapshot.Result, &stored); err != nil {
			return nil, err
		}
		result.AccessToken = stored.AccessToken
		result.TokenType = "Bearer"
		result.ExpiresIn = stored.ExpiresIn
		result.Scope = stored.Scope
		result.RefreshToken = stored.RefreshToken
		result.RefreshTokenExpiresIn = stored.RefreshTokenExpiresIn
		result.RetryAfter = 0
	case oauthqueue.StatusFailed:
		result.Error = "invalid_grant"
		result.ErrorDescription = snapshot.Error
		result.Reauthorize = true
		if snapshot.Error == oauthQueuedClientUnavailable {
			result.Error = "invalid_client"
			result.Reauthorize = false
		}
		result.RetryAfter = 0
	case oauthqueue.StatusUnknown:
		result.Error = "result_unknown"
		result.ErrorDescription = snapshot.Error
		result.Reauthorize = true
		result.RetryAfter = 0
	case oauthqueue.StatusExpired:
		result.Error = "expired"
		result.ErrorDescription = "authorization exchange expired; please re-authorize"
		result.Reauthorize = true
		result.RetryAfter = 0
	}
	return result, nil
}

func AcquireOAuthExchangeAdmission(ctx context.Context) (*OAuthExchangeAdmission, error) {
	runtime := currentOAuthQueue()
	if runtime == nil {
		return &OAuthExchangeAdmission{startedAt: time.Now()}, nil
	}
	owner := runtime.owner + ":direct:" + common.GetUUID()
	permit, err := runtime.waitForPermit(ctx, owner)
	if err != nil {
		return nil, err
	}
	dbWaitBefore := int64(0)
	if sqlDB, statsErr := model.DB.DB(); statsErr == nil {
		dbWaitBefore = sqlDB.Stats().WaitCount
	}
	admission := &OAuthExchangeAdmission{
		runtime:      runtime,
		permit:       permit,
		startedAt:    time.Now(),
		dbWaitBefore: dbWaitBefore,
		renewDone:    make(chan struct{}),
	}
	go admission.renewPermit()
	return admission, nil
}

func AcquireOAuthValidationAdmission(ctx context.Context) (*OAuthValidationAdmission, error) {
	runtime := currentOAuthQueue()
	if runtime == nil {
		return &OAuthValidationAdmission{}, nil
	}
	if runtime.validationWait.Add(1) > int64(runtime.queue.Config().Capacity) {
		runtime.validationWait.Add(-1)
		return nil, oauthqueue.ErrQueueFull
	}
	defer runtime.validationWait.Add(-1)
	timer := time.NewTimer(runtime.queue.Config().JobTTL)
	defer timer.Stop()
	select {
	case runtime.validationSlots <- struct{}{}:
		exchange, err := AcquireOAuthExchangeAdmission(ctx)
		if err != nil {
			<-runtime.validationSlots
			return nil, err
		}
		return &OAuthValidationAdmission{runtime: runtime, exchange: exchange}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-runtime.stop:
		return nil, fmt.Errorf("OAuth exchange queue is stopping")
	case <-timer.C:
		return nil, fmt.Errorf("OAuth validation capacity timed out")
	}
}

func (admission *OAuthValidationAdmission) Finish() {
	if admission == nil || admission.runtime == nil || !admission.finished.CompareAndSwap(false, true) {
		return
	}
	<-admission.runtime.validationSlots
	admission.exchange.Finish(false)
}

func (admission *OAuthExchangeAdmission) Finish(failed bool) {
	if admission == nil || !admission.finished.CompareAndSwap(false, true) {
		return
	}
	if admission.runtime == nil || admission.permit == nil {
		return
	}
	close(admission.renewDone)
	poolWait := false
	if sqlDB, err := model.DB.DB(); err == nil {
		poolWait = sqlDB.Stats().WaitCount > admission.dbWaitBefore
	}
	releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
	releaseErr := admission.runtime.queue.ReleasePermit(releaseCtx, admission.permit)
	releaseCancel()
	admission.runtime.logQueueError("release direct permit", releaseErr)
	metricsCtx, metricsCancel := context.WithTimeout(context.Background(), 2*time.Second)
	metricsErr := admission.runtime.queue.RecordOutcome(metricsCtx, oauthqueue.Outcome{
		Duration: time.Since(admission.startedAt),
		Failed:   failed,
		PoolWait: poolWait,
	})
	metricsCancel()
	admission.runtime.logQueueError("record outcome", metricsErr)
}

func (admission *OAuthExchangeAdmission) renewPermit() {
	ticker := time.NewTicker(admission.runtime.queue.Config().LeaseTTL / 3)
	defer ticker.Stop()
	for {
		select {
		case <-admission.renewDone:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := admission.runtime.queue.RenewPermit(ctx, admission.permit)
			cancel()
			if err != nil {
				admission.runtime.logQueueError("renew direct permit", err)
				return
			}
		}
	}
}

func currentOAuthQueue() *oauthQueueRuntime {
	oauthQueueMu.RLock()
	runtime := oauthQueueCurrent
	oauthQueueMu.RUnlock()
	return runtime
}

func (runtime *oauthQueueRuntime) waitForPermit(ctx context.Context, owner string) (*oauthqueue.Permit, error) {
	for {
		permit, err := runtime.queue.AcquirePermit(ctx, owner)
		if err != nil {
			return nil, err
		}
		if permit != nil {
			return permit, nil
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-runtime.stop:
			timer.Stop()
			return nil, fmt.Errorf("OAuth exchange queue is stopping")
		case <-timer.C:
		}
	}
}

func (runtime *oauthQueueRuntime) workerLoop(workerIndex int) {
	defer runtime.wg.Done()
	partition := workerIndex % runtime.queue.Config().Partitions
	owner := fmt.Sprintf("%s:worker:%d", runtime.owner, workerIndex)
	for {
		select {
		case <-runtime.stop:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		pending, err := runtime.queue.Pending(ctx, partition)
		cancel()
		if err != nil {
			runtime.logQueueError("read pending jobs", err)
		}
		if err != nil || pending == 0 {
			partition = (partition + 1) % runtime.queue.Config().Partitions
			if !runtime.sleep(100 * time.Millisecond) {
				return
			}
			continue
		}
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		permit, err := runtime.queue.AcquirePermit(ctx, owner)
		cancel()
		if err != nil {
			runtime.logQueueError("acquire worker permit", err)
		}
		if err != nil || permit == nil {
			if !runtime.sleep(25 * time.Millisecond) {
				return
			}
			continue
		}
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		job, err := runtime.queue.Claim(ctx, partition, owner, permit.Fence)
		cancel()
		if err != nil {
			runtime.logQueueError("claim job", err)
		}
		if err != nil || job == nil {
			ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
			_ = runtime.queue.ReleasePermit(ctx, permit)
			cancel()
			partition = (partition + 1) % runtime.queue.Config().Partitions
			continue
		}
		runtime.processJob(job, permit)
		partition = (partition + 1) % runtime.queue.Config().Partitions
	}
}

func (runtime *oauthQueueRuntime) processJob(job *oauthqueue.ClaimedJob, permit *oauthqueue.Permit) {
	startedAt := time.Now()
	failed := false
	poolWait := false
	permitReleased := false
	releasePermit := func() {
		if permitReleased {
			return
		}
		permitReleased = true
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		releaseErr := runtime.queue.ReleasePermit(releaseCtx, permit)
		releaseCancel()
		runtime.logQueueError("release worker permit", releaseErr)
		metricsCtx, metricsCancel := context.WithTimeout(context.Background(), 2*time.Second)
		metricsErr := runtime.queue.RecordOutcome(metricsCtx, oauthqueue.Outcome{Duration: time.Since(startedAt), Failed: failed, PoolWait: poolWait})
		metricsCancel()
		runtime.logQueueError("record outcome", metricsErr)
	}
	defer releasePermit()
	if job.Attempt > runtime.queue.Config().MaxAttempts {
		failed = true
		runtime.finishJob(job, oauthqueue.StatusUnknown, "", "authorization exchange exceeded the retry limit")
		return
	}
	var payload queuedOAuthExchangePayload
	if err := decryptOAuthQueueValue(job.Payload, &payload); err != nil {
		failed = true
		runtime.finishJob(job, oauthqueue.StatusFailed, "", "authorization exchange payload is invalid")
		return
	}
	deadline := time.UnixMilli(payload.DeadlineUnixMilli)
	if !deadline.After(time.Now()) {
		runtime.finishJob(job, oauthqueue.StatusExpired, "", "authorization exchange expired")
		return
	}
	processCtx, cancelProcess := context.WithDeadline(context.Background(), deadline)
	defer cancelProcess()
	renewDone := make(chan struct{})
	go runtime.renewLeases(processCtx, renewDone, job, permit)
	beforeWait := int64(0)
	if sqlDB, err := model.DB.DB(); err == nil {
		beforeWait = sqlDB.Stats().WaitCount
	}
	exchange, err := ExchangeOAuthAuthorizationCode(
		processCtx,
		payload.Code,
		payload.ClientID,
		payload.RedirectURI,
		payload.CodeChallenge,
		payload.CodeChallengeMethod,
		time.Now(),
	)
	close(renewDone)
	if sqlDB, statsErr := model.DB.DB(); statsErr == nil {
		poolWait = sqlDB.Stats().WaitCount > beforeWait
	}
	if err != nil {
		if errors.Is(err, ErrOAuthTokenClientUnavailable) {
			runtime.finishJob(job, oauthqueue.StatusFailed, "", oauthQueuedClientUnavailable)
			return
		}
		if errors.Is(err, ErrOAuthTokenUserUnavailable) {
			runtime.finishJob(job, oauthqueue.StatusFailed, "", "user is disabled or not found")
			return
		}
		failed = true
		// The job was validated before enqueue. An invalid code here may mean a
		// previous worker committed but lost its result, so never replay it.
		runtime.finishJob(job, oauthqueue.StatusUnknown, "", "authorization result is unknown; please re-authorize")
		return
	}
	storedResult, err := encryptOAuthQueueValue(queuedOAuthExchangeResult{
		AccessToken:           exchange.AccessToken,
		ExpiresIn:             exchange.AccessTokenExpiresIn,
		Scope:                 exchange.Scope,
		RefreshToken:          exchange.RefreshToken,
		RefreshTokenExpiresIn: exchange.RefreshTokenExpiresIn,
	})
	if err != nil {
		failed = true
		runtime.finishJob(job, oauthqueue.StatusUnknown, "", "authorization result could not be stored; please re-authorize")
		return
	}
	app := &model.OAuthApp{
		Id:         payload.AppID,
		Name:       payload.AppName,
		ClientId:   payload.ClientID,
		ClientType: payload.AppClientType,
	}
	runtime.finishJob(job, oauthqueue.StatusSucceeded, storedResult, "")
	releasePermit()
	auditCtx, auditCancel := context.WithTimeout(context.Background(), 2*time.Second)
	model.RecordOAuthTokenIssueOperationLog(
		auditCtx,
		exchange.User,
		app,
		exchange.Grant,
		exchange.Scope,
		exchange.RedirectURI,
		exchange.AccessTokenExpiresIn,
		payload.RequestID,
		payload.ClientIP,
		payload.UserAgent,
	)
	auditCancel()
}

func (runtime *oauthQueueRuntime) renewLeases(ctx context.Context, done <-chan struct{}, job *oauthqueue.ClaimedJob, permit *oauthqueue.Permit) {
	ticker := time.NewTicker(runtime.queue.Config().LeaseTTL / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			jobCtx, jobCancel := context.WithTimeout(context.Background(), 2*time.Second)
			jobErr := runtime.queue.Renew(jobCtx, job)
			jobCancel()
			permitCtx, permitCancel := context.WithTimeout(context.Background(), 2*time.Second)
			permitErr := runtime.queue.RenewPermit(permitCtx, permit)
			permitCancel()
			if jobErr != nil {
				runtime.logQueueError("renew job lease", jobErr)
				return
			}
			if permitErr != nil {
				runtime.logQueueError("renew worker permit", permitErr)
				return
			}
		}
	}
}

func (runtime *oauthQueueRuntime) finishJob(job *oauthqueue.ClaimedJob, status oauthqueue.Status, result string, errorDescription string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runtime.queue.Finish(ctx, job, status, result, errorDescription); err != nil {
		runtime.logQueueError("finish job", err)
	}
}

func (runtime *oauthQueueRuntime) maintenanceLoop() {
	defer runtime.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-runtime.stop:
			return
		case <-ticker.C:
			for partition := 0; partition < runtime.queue.Config().Partitions; partition++ {
				select {
				case <-runtime.stop:
					return
				default:
				}
				expireCtx, expireCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, expireErr := runtime.queue.ExpirePending(expireCtx, partition, oauthQueueMaintenanceBatch)
				expireCancel()
				runtime.logQueueError("expire pending jobs", expireErr)
				reclaimCtx, reclaimCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, reclaimErr := runtime.queue.Reclaim(reclaimCtx, partition, oauthQueueMaintenanceBatch)
				reclaimCancel()
				runtime.logQueueError("reclaim jobs", reclaimErr)
				for batch := 0; batch < oauthQueueCleanupMaxBatches; batch++ {
					cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
					cleaned, err := runtime.queue.Cleanup(cleanupCtx, partition, oauthQueueMaintenanceBatch)
					cleanupCancel()
					runtime.logQueueError("clean up jobs", err)
					if err != nil || cleaned < oauthQueueMaintenanceBatch {
						break
					}
				}
			}
		}
	}
}

func (runtime *oauthQueueRuntime) adaptiveLoop() {
	defer runtime.wg.Done()
	ticker := time.NewTicker(runtime.queue.Config().AdjustInterval)
	defer ticker.Stop()
	for {
		select {
		case <-runtime.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			adjustment, err := runtime.queue.AdjustConcurrency(ctx, runtime.owner)
			if err != nil {
				common.SysError("adjust OAuth exchange concurrency failed: " + err.Error())
			} else if adjustment.Next != adjustment.Previous {
				common.SysLog(fmt.Sprintf(
					"OAuth exchange concurrency adjusted: previous=%d next=%d total=%d failed=%d pool_wait=%d p95=%s",
					adjustment.Previous,
					adjustment.Next,
					adjustment.Total,
					adjustment.Failed,
					adjustment.PoolWait,
					adjustment.P95,
				))
			}
			cancel()
		}
	}
}

func (runtime *oauthQueueRuntime) sleep(duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-runtime.stop:
		return false
	case <-timer.C:
		return true
	}
}

func (runtime *oauthQueueRuntime) logQueueError(operation string, err error) {
	if err == nil {
		return
	}
	now := time.Now().Unix()
	previous := runtime.errorLogAt.Load()
	if now-previous >= 5 && runtime.errorLogAt.CompareAndSwap(previous, now) {
		common.SysError(fmt.Sprintf("OAuth exchange queue %s failed: %v", operation, err))
	}
}

func encryptOAuthQueueValue(value any) (string, error) {
	plaintext, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	aead, err := oauthQueueAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, []byte("gemai-oauth-exchange-v1"))
	return base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

func decryptOAuthQueueValue(encoded string, destination any) error {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return err
	}
	aead, err := oauthQueueAEAD()
	if err != nil {
		return err
	}
	if len(data) < aead.NonceSize() {
		return fmt.Errorf("OAuth queue ciphertext is invalid")
	}
	plaintext, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("gemai-oauth-exchange-v1"))
	if err != nil {
		return err
	}
	return common.Unmarshal(plaintext, destination)
}

func oauthQueueAEAD() (cipher.AEAD, error) {
	key := sha256.Sum256([]byte("gemai-oauth-exchange-v1:" + common.CryptoSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func OAuthPollTokenFromAuthorizationHeader(header string) string {
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}
