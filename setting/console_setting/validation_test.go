package console_setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAnnouncementsCountsUnicodeCharacters(t *testing.T) {
	publishDate := "2026-07-11T00:00:00+08:00"
	contentAtLimit := strings.Repeat("公告", maxAnnouncementContentRunes/2)
	announcements := []map[string]interface{}{
		{
			"id":          1,
			"content":     contentAtLimit,
			"publishDate": publishDate,
			"type":        "default",
		},
	}

	data, err := common.Marshal(announcements)
	require.NoError(t, err)
	assert.NoError(t, validateAnnouncements(string(data)))

	announcements[0]["content"] = contentAtLimit + "超"
	data, err = common.Marshal(announcements)
	require.NoError(t, err)

	err = validateAnnouncements(string(data))
	require.Error(t, err)
	assert.ErrorContains(t, err, "10000字符")
	assert.ErrorContains(t, err, publishDate)
}

func TestValidateAnnouncementsExtraCountsUnicodeCharacters(t *testing.T) {
	announcements := []map[string]interface{}{
		{
			"content":     "维护通知",
			"publishDate": "2026-07-11T00:00:00+08:00",
			"type":        "warning",
			"extra":       strings.Repeat("补", maxAnnouncementExtraRunes),
		},
	}

	data, err := common.Marshal(announcements)
	require.NoError(t, err)
	assert.NoError(t, validateAnnouncements(string(data)))

	announcements[0]["extra"] = strings.Repeat(
		"补",
		maxAnnouncementExtraRunes+1,
	)
	data, err = common.Marshal(announcements)
	require.NoError(t, err)

	err = validateAnnouncements(string(data))
	require.Error(t, err)
	assert.ErrorContains(t, err, "500字符")
}

func TestValidateAnnouncementsLimitsAggregatePayloadSize(t *testing.T) {
	announcements := make([]map[string]interface{}, maxAnnouncementCount)
	for i := range announcements {
		announcements[i] = map[string]interface{}{
			"content":     strings.Repeat("中", 2_000),
			"publishDate": "2026-07-11T00:00:00+08:00",
			"type":        "default",
		}
	}

	data, err := common.Marshal(announcements)
	require.NoError(t, err)
	require.Greater(t, len(data), maxAnnouncementsJSONBytes)

	err = validateAnnouncements(string(data))
	require.Error(t, err)
	assert.ErrorContains(t, err, "512KB")
}
