package controller

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/console_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

var completionRatioMetaOptionKeys = []string{
	"ModelPrice",
	"ModelRatio",
	"CompletionRatio",
	"CacheRatio",
	"CreateCacheRatio",
	"ImageRatio",
	"AudioRatio",
	"AudioCompletionRatio",
}

type CustomScriptSetting struct {
	Scripts []CustomScriptEntry `json:"scripts"`
}

type CustomScriptEntry struct {
	Src   string            `json:"src"`
	ID    string            `json:"id,omitempty"`
	Async bool              `json:"async,omitempty"`
	Defer bool              `json:"defer,omitempty"`
	Data  map[string]string `json:"data,omitempty"`
}

type CustomScriptAllowedRulesSetting struct {
	Rules []CustomScriptAllowedRule `json:"rules"`
}

type CustomScriptAllowedRule struct {
	Src      string   `json:"src"`
	DataKeys []string `json:"data_keys,omitempty"`
}

func normalizeCustomScriptDataKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return strings.TrimPrefix(normalized, "data-")
}

func isValidCustomScriptDataKey(key string) bool {
	if len(key) == 0 || len(key) > 64 {
		return false
	}
	for _, c := range key {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			continue
		}
		return false
	}
	return true
}

func parseAndNormalizeCustomScriptAllowedRules(raw string) (CustomScriptAllowedRulesSetting, map[string]map[string]struct{}, error) {
	var setting CustomScriptAllowedRulesSetting
	if err := common.UnmarshalJsonStr(raw, &setting); err != nil {
		return setting, nil, errors.New("自定义脚本白名单规则必须是有效 JSON")
	}
	if len(setting.Rules) == 0 {
		return setting, nil, errors.New("自定义脚本白名单规则不能为空")
	}
	if len(setting.Rules) > 50 {
		return setting, nil, errors.New("自定义脚本白名单规则最多允许 50 条")
	}

	normalizedRules := make([]CustomScriptAllowedRule, 0, len(setting.Rules))
	ruleMap := make(map[string]map[string]struct{}, len(setting.Rules))
	for i, rule := range setting.Rules {
		index := i + 1
		rule.Src = strings.TrimSpace(rule.Src)
		if rule.Src == "" {
			return setting, nil, fmt.Errorf("第 %d 条白名单规则缺少 src", index)
		}

		parsedURL, err := url.Parse(rule.Src)
		if err != nil || !parsedURL.IsAbs() {
			return setting, nil, fmt.Errorf("第 %d 条白名单规则 src 无效", index)
		}
		if strings.ToLower(parsedURL.Scheme) != "https" {
			return setting, nil, fmt.Errorf("第 %d 条白名单规则 src 必须使用 https", index)
		}
		normalizedSrc := parsedURL.String()
		if _, exists := ruleMap[normalizedSrc]; exists {
			return setting, nil, fmt.Errorf("白名单规则中存在重复的 src: %s", normalizedSrc)
		}

		normalizedDataKeys := make([]string, 0, len(rule.DataKeys))
		allowedDataKeys := make(map[string]struct{}, len(rule.DataKeys))
		for _, key := range rule.DataKeys {
			normalizedKey := normalizeCustomScriptDataKey(key)
			if !isValidCustomScriptDataKey(normalizedKey) {
				return setting, nil, fmt.Errorf("白名单规则 %s 中存在无效 data-* 参数名: %s", normalizedSrc, key)
			}
			if _, exists := allowedDataKeys[normalizedKey]; exists {
				continue
			}
			allowedDataKeys[normalizedKey] = struct{}{}
			normalizedDataKeys = append(normalizedDataKeys, normalizedKey)
		}

		ruleMap[normalizedSrc] = allowedDataKeys
		normalizedRules = append(normalizedRules, CustomScriptAllowedRule{
			Src:      normalizedSrc,
			DataKeys: normalizedDataKeys,
		})
	}

	setting.Rules = normalizedRules
	return setting, ruleMap, nil
}

func validateAndNormalizeCustomScriptAllowedRules(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("自定义脚本白名单规则不能为空")
	}
	setting, _, err := parseAndNormalizeCustomScriptAllowedRules(trimmed)
	if err != nil {
		return "", err
	}
	jsonBytes, err := common.Marshal(setting)
	if err != nil {
		return "", errors.New("自定义脚本白名单规则处理失败")
	}
	return string(jsonBytes), nil
}

func getCustomScriptAllowedRuleMap() (map[string]map[string]struct{}, error) {
	trimmed := strings.TrimSpace(common.CustomScriptAllowedRules)
	if trimmed == "" {
		return nil, errors.New("自定义脚本白名单规则为空")
	}
	_, ruleMap, err := parseAndNormalizeCustomScriptAllowedRules(trimmed)
	if err != nil {
		return nil, errors.New("自定义脚本白名单规则无效，请先更新合法规则")
	}
	return ruleMap, nil
}

func validateAndNormalizeCustomScript(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	var setting CustomScriptSetting
	if err := common.UnmarshalJsonStr(trimmed, &setting); err != nil {
		return "", errors.New("自定义脚本配置必须是有效 JSON")
	}
	if len(setting.Scripts) == 0 {
		return "", errors.New("自定义脚本配置不能为空")
	}
	if len(setting.Scripts) > 5 {
		return "", errors.New("自定义脚本最多允许配置 5 个")
	}

	allowedRuleMap, err := getCustomScriptAllowedRuleMap()
	if err != nil {
		return "", err
	}

	normalizedScripts := make([]CustomScriptEntry, 0, len(setting.Scripts))
	for i, script := range setting.Scripts {
		index := i + 1
		script.Src = strings.TrimSpace(script.Src)
		if script.Src == "" {
			return "", fmt.Errorf("第 %d 个脚本缺少 src", index)
		}

		parsedURL, err := url.Parse(script.Src)
		if err != nil || !parsedURL.IsAbs() {
			return "", fmt.Errorf("第 %d 个脚本地址无效", index)
		}
		if strings.ToLower(parsedURL.Scheme) != "https" {
			return "", fmt.Errorf("第 %d 个脚本必须使用 https", index)
		}
		normalizedSrc := parsedURL.String()
		allowedDataKeys, ok := allowedRuleMap[normalizedSrc]
		if !ok {
			return "", fmt.Errorf("不允许的脚本地址: %s", normalizedSrc)
		}
		script.ID = strings.TrimSpace(script.ID)
		if len(script.ID) > 128 {
			return "", fmt.Errorf("第 %d 个脚本 id 过长", index)
		}

		normalizedData := make(map[string]string, len(script.Data))
		for key, value := range script.Data {
			normalizedKey := normalizeCustomScriptDataKey(key)
			if !isValidCustomScriptDataKey(normalizedKey) {
				return "", fmt.Errorf("第 %d 个脚本存在无效的 data-* 参数名: %s", index, key)
			}
			if _, ok := allowedDataKeys[normalizedKey]; !ok {
				return "", fmt.Errorf("脚本 %s 不允许 data-%s 参数", normalizedSrc, normalizedKey)
			}
			normalizedValue := strings.TrimSpace(value)
			if len(normalizedValue) > 4096 {
				return "", fmt.Errorf("脚本 %s 的 data-%s 参数过长", normalizedSrc, normalizedKey)
			}
			normalizedData[normalizedKey] = normalizedValue
		}

		script.Src = normalizedSrc
		script.Data = normalizedData
		normalizedScripts = append(normalizedScripts, script)
	}

	setting.Scripts = normalizedScripts
	jsonBytes, err := common.Marshal(setting)
	if err != nil {
		return "", errors.New("自定义脚本配置处理失败")
	}
	return string(jsonBytes), nil
}

func collectModelNamesFromOptionValue(raw string, modelNames map[string]struct{}) {
	if strings.TrimSpace(raw) == "" {
		return
	}

	var parsed map[string]any
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return
	}

	for modelName := range parsed {
		modelNames[modelName] = struct{}{}
	}
}

func buildCompletionRatioMetaValue(optionValues map[string]string) string {
	modelNames := make(map[string]struct{})
	for _, key := range completionRatioMetaOptionKeys {
		collectModelNamesFromOptionValue(optionValues[key], modelNames)
	}

	meta := make(map[string]ratio_setting.CompletionRatioInfo, len(modelNames))
	for modelName := range modelNames {
		meta[modelName] = ratio_setting.GetCompletionRatioInfo(modelName)
	}

	jsonBytes, err := common.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(jsonBytes)
}

func GetOptions(c *gin.Context) {
	var options []*model.Option
	optionValues := make(map[string]string)
	common.OptionMapRWMutex.Lock()
	for k, v := range common.OptionMap {
		value := common.Interface2String(v)
		if strings.HasSuffix(k, "Token") ||
			strings.HasSuffix(k, "Secret") ||
			strings.HasSuffix(k, "Key") ||
			strings.HasSuffix(k, "secret") ||
			strings.HasSuffix(k, "api_key") {
			continue
		}
		options = append(options, &model.Option{
			Key:   k,
			Value: value,
		})
		for _, optionKey := range completionRatioMetaOptionKeys {
			if optionKey == k {
				optionValues[k] = value
				break
			}
		}
	}
	common.OptionMapRWMutex.Unlock()
	options = append(options, &model.Option{
		Key:   "CompletionRatioMeta",
		Value: buildCompletionRatioMetaValue(optionValues),
	})
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    options,
	})
	return
}

type OptionUpdateRequest struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
}

func UpdateOption(c *gin.Context) {
	var option OptionUpdateRequest
	err := common.DecodeJson(c.Request.Body, &option)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的参数",
		})
		return
	}
	switch option.Value.(type) {
	case bool:
		option.Value = common.Interface2String(option.Value.(bool))
	case float64:
		option.Value = common.Interface2String(option.Value.(float64))
	case int:
		option.Value = common.Interface2String(option.Value.(int))
	default:
		option.Value = fmt.Sprintf("%v", option.Value)
	}
	switch option.Key {
	case "GitHubOAuthEnabled":
		if option.Value == "true" && common.GitHubClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 GitHub OAuth，请先填入 GitHub Client Id 以及 GitHub Client Secret！",
			})
			return
		}
	case "discord.enabled":
		if option.Value == "true" && system_setting.GetDiscordSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Discord OAuth，请先填入 Discord Client Id 以及 Discord Client Secret！",
			})
			return
		}
	case "oidc.enabled":
		if option.Value == "true" && system_setting.GetOIDCSettings().ClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 OIDC 登录，请先填入 OIDC Client Id 以及 OIDC Client Secret！",
			})
			return
		}
	case "LinuxDOOAuthEnabled":
		if option.Value == "true" && common.LinuxDOClientId == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 LinuxDO OAuth，请先填入 LinuxDO Client Id 以及 LinuxDO Client Secret！",
			})
			return
		}
	case "EmailDomainRestrictionEnabled":
		if option.Value == "true" && len(common.EmailDomainWhitelist) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用邮箱域名限制，请先填入限制的邮箱域名！",
			})
			return
		}
	case "WeChatAuthEnabled":
		if option.Value == "true" && common.WeChatServerAddress == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用微信登录，请先填入微信登录相关配置信息！",
			})
			return
		}
	case "TurnstileCheckEnabled":
		if option.Value == "true" && common.TurnstileSiteKey == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Turnstile 校验，请先填入 Turnstile 校验相关配置信息！",
			})

			return
		}
	case "TelegramOAuthEnabled":
		if option.Value == "true" && common.TelegramBotToken == "" {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "无法启用 Telegram OAuth，请先填入 Telegram Bot Token！",
			})
			return
		}
	case "GroupRatio":
		err = ratio_setting.CheckGroupRatio(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "ImageRatio":
		err = ratio_setting.UpdateImageRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "图片倍率设置失败: " + err.Error(),
			})
			return
		}
	case "AudioRatio":
		err = ratio_setting.UpdateAudioRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "音频倍率设置失败: " + err.Error(),
			})
			return
		}
	case "AudioCompletionRatio":
		err = ratio_setting.UpdateAudioCompletionRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "音频补全倍率设置失败: " + err.Error(),
			})
			return
		}
	case "CreateCacheRatio":
		err = ratio_setting.UpdateCreateCacheRatioByJSONString(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": "缓存创建倍率设置失败: " + err.Error(),
			})
			return
		}
	case "ModelRequestRateLimitGroup":
		err = setting.CheckModelRequestRateLimitGroup(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticDisableStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "AutomaticRetryStatusCodes":
		_, err = operation_setting.ParseHTTPStatusCodeRanges(option.Value.(string))
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.api_info":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "ApiInfo")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.announcements":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "Announcements")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.faq":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "FAQ")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "console_setting.uptime_kuma_groups":
		err = console_setting.ValidateConsoleSettings(option.Value.(string), "UptimeKumaGroups")
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": err.Error(),
			})
			return
		}
	case "CustomScriptAllowedRules":
		normalizedValue, validateErr := validateAndNormalizeCustomScriptAllowedRules(option.Value.(string))
		if validateErr != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": validateErr.Error(),
			})
			return
		}
		option.Value = normalizedValue
	case "CustomScript":
		normalizedValue, validateErr := validateAndNormalizeCustomScript(option.Value.(string))
		if validateErr != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"message": validateErr.Error(),
			})
			return
		}
		option.Value = normalizedValue
	}
	err = model.UpdateOption(option.Key, option.Value.(string))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
	})
	return
}
