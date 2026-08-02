package sora

import (
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTaskResultErrorShapes(t *testing.T) {
	cases := []struct {
		name           string
		body           string
		expectedStatus string
		expectedReason string
	}{
		{
			name:           "error as object",
			body:           `{"id":"video_1","status":"failed","error":{"message":"content policy violation","code":"moderation_blocked"}}`,
			expectedStatus: model.TaskStatusFailure,
			expectedReason: "content policy violation",
		},
		{
			name:           "error as string",
			body:           `{"id":"video_1","status":"failed","error":"upstream generation failed"}`,
			expectedStatus: model.TaskStatusFailure,
			expectedReason: "upstream generation failed",
		},
		{
			name:           "error as empty string",
			body:           `{"id":"video_1","status":"failed","error":""}`,
			expectedStatus: model.TaskStatusFailure,
			expectedReason: "task failed",
		},
		{
			name:           "error absent",
			body:           `{"id":"video_1","status":"cancelled"}`,
			expectedStatus: model.TaskStatusFailure,
			expectedReason: "task failed",
		},
		{
			name:           "error null on success",
			body:           `{"id":"video_1","status":"completed","error":null}`,
			expectedStatus: model.TaskStatusSuccess,
			expectedReason: "",
		},
	}

	adaptor := &TaskAdaptor{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := adaptor.ParseTaskResult([]byte(tc.body))
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tc.expectedStatus, result.Status)
			assert.Equal(t, tc.expectedReason, result.Reason)
		})
	}
}
