package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTransactionalResponseWriterDefersAcceptedResponseUntilCommit(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	originalWriter := context.Writer
	bufferedWriter := newTransactionalResponseWriter(originalWriter, nil, nil)
	context.Writer = bufferedWriter

	context.JSON(http.StatusAccepted, gin.H{"id": "task-1"})

	assert.Empty(t, recorder.Body.String())
	assert.Equal(t, http.StatusAccepted, bufferedWriter.Status())
	assert.True(t, bufferedWriter.Written())
	require.NoError(t, bufferedWriter.flushToClient())
	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.JSONEq(t, `{"id":"task-1"}`, recorder.Body.String())
}

func TestTransactionalResponseWriterCanDiscardResponseAfterCommitFailure(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	originalWriter := context.Writer
	bufferedWriter := newTransactionalResponseWriter(originalWriter, nil, nil)
	context.Writer = bufferedWriter

	context.JSON(http.StatusOK, gin.H{"id": "task-1"})
	bufferedWriter.Header().Set("X-Upstream-Task", "accepted")
	context.Writer = originalWriter
	context.JSON(http.StatusInternalServerError, gin.H{"error": "commit failed"})

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.JSONEq(t, `{"error":"commit failed"}`, recorder.Body.String())
	assert.Empty(t, recorder.Header().Get("X-Upstream-Task"))
}

func TestTransactionalResponseWriterCommitsBufferedHeaders(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	bufferedWriter := newTransactionalResponseWriter(context.Writer, nil, nil)
	context.Writer = bufferedWriter

	context.Header("X-Upstream-Task", "accepted")
	context.JSON(http.StatusAccepted, gin.H{"id": "task-1"})

	assert.Empty(t, recorder.Header().Get("X-Upstream-Task"))
	require.NoError(t, bufferedWriter.flushToClient())
	assert.Equal(t, "accepted", recorder.Header().Get("X-Upstream-Task"))
}

func TestTransactionalResponseWriterSwitchesToPassthroughForStreaming(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	passthrough := false
	bufferedWriter := newTransactionalResponseWriter(context.Writer, func() bool { return passthrough }, nil)
	context.Writer = bufferedWriter

	context.Header("Content-Type", "text/event-stream")
	_, err := context.Writer.WriteString("data: first\n\n")
	require.NoError(t, err)
	assert.Empty(t, recorder.Body.String())

	passthrough = true
	context.Writer.Flush()
	assert.Equal(t, "data: first\n\n", recorder.Body.String())
	_, err = context.Writer.WriteString("data: second\n\n")
	require.NoError(t, err)
	assert.Equal(t, "data: first\n\ndata: second\n\n", recorder.Body.String())
}

func TestTransactionalResponseWriterDiscardsFailedRetryAttempt(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	bufferedWriter := newTransactionalResponseWriter(context.Writer, nil, nil)
	context.Writer = bufferedWriter

	context.Header("X-Upstream-Attempt", "first")
	context.JSON(http.StatusBadGateway, gin.H{"error": "first failed"})
	bufferedWriter.reset()
	context.Header("X-Upstream-Attempt", "second")
	context.JSON(http.StatusOK, gin.H{"id": "accepted"})

	require.NoError(t, bufferedWriter.flushToClient())
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "second", recorder.Header().Get("X-Upstream-Attempt"))
	assert.JSONEq(t, `{"id":"accepted"}`, recorder.Body.String())
}

func TestTransactionalResponseWriterRetainsMaterializedBodyUntilCommit(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	bufferedWriter := newTransactionalResponseWriter(context.Writer, nil, nil)
	context.Writer = bufferedWriter

	body := []byte("large materialized response")
	_, err := bufferedWriter.WriteDeferredBytes(body)
	require.NoError(t, err)
	assert.Equal(t, len(body), bufferedWriter.Size())
	assert.Empty(t, recorder.Body.String())

	require.NoError(t, bufferedWriter.flushToClient())
	assert.Equal(t, string(body), recorder.Body.String())
}

func TestTransactionalResponseWriterDefersBodyAfterPaddingStops(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	paddingActive := true
	bufferedWriter := newTransactionalResponseWriter(context.Writer, nil, func() bool { return paddingActive })
	context.Writer = bufferedWriter

	context.Writer.WriteHeader(http.StatusOK)
	_, err := context.Writer.WriteString(" ")
	require.NoError(t, err)
	context.Writer.Flush()
	assert.Equal(t, " ", recorder.Body.String())

	paddingActive = false
	_, err = context.Writer.WriteString(`{"id":"settled"}`)
	require.NoError(t, err)
	context.Writer.Flush()
	assert.Equal(t, " ", recorder.Body.String())

	require.NoError(t, bufferedWriter.flushToClient())
	assert.Equal(t, ` {"id":"settled"}`, recorder.Body.String())
}

func TestTransactionalResponseWriterCanSwitchFromPaddingToStreaming(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	paddingActive := true
	streaming := false
	bufferedWriter := newTransactionalResponseWriter(
		context.Writer,
		func() bool { return streaming },
		func() bool { return paddingActive },
	)
	context.Writer = bufferedWriter

	_, err := context.Writer.WriteString(" ")
	require.NoError(t, err)
	paddingActive = false
	streaming = true
	_, err = context.Writer.WriteString("data: live\n\n")
	require.NoError(t, err)

	assert.Equal(t, " data: live\n\n", recorder.Body.String())
}
