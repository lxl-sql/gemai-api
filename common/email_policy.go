package common

import (
	"fmt"
	"regexp"
	"strings"
)

var emailRuleDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func EmailPolicyRequiresEmail() bool {
	return EmailDomainRestrictionEnabled || EmailAliasRestrictionEnabled || strings.TrimSpace(EmailLocalPartRules) != ""
}

func ValidateEmailLocalPartRulesConfig(value string) error {
	_, err := parseEmailLocalPartRules(value)
	return err
}

func ValidateEmailPolicy(email string) (string, error) {
	normalizedEmail := NormalizeEmail(email)
	if err := Validate.Var(normalizedEmail, "required,email"); err != nil {
		return "", fmt.Errorf("无效的邮箱地址")
	}

	localPart, domainPart, ok := splitEmail(normalizedEmail)
	if !ok {
		return "", fmt.Errorf("无效的邮箱地址")
	}

	if EmailDomainRestrictionEnabled && !isEmailDomainAllowed(domainPart) {
		return "", fmt.Errorf("管理员已启用邮箱域名白名单，该邮箱域名不允许注册")
	}

	if EmailAliasRestrictionEnabled && (strings.Contains(localPart, "+") || strings.Contains(localPart, ".")) {
		return "", fmt.Errorf("管理员已启用邮箱地址别名限制，该邮箱地址包含不允许的特殊符号")
	}

	rules, err := parseEmailLocalPartRules(EmailLocalPartRules)
	if err != nil {
		return "", err
	}
	if rule, ok := rules[domainPart]; ok && !rule.MatchString(localPart) {
		return "", fmt.Errorf("该邮箱地址不符合管理员配置的邮箱规则")
	}

	return normalizedEmail, nil
}

func splitEmail(email string) (string, string, bool) {
	parts := strings.Split(email, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func isEmailDomainAllowed(domainPart string) bool {
	for _, domain := range EmailDomainWhitelist {
		if domainPart == strings.ToLower(strings.TrimSpace(domain)) {
			return true
		}
	}
	return false
}

func parseEmailLocalPartRules(value string) (map[string]*regexp.Regexp, error) {
	rules := make(map[string]*regexp.Regexp)
	for lineNumber, rawLine := range strings.Split(value, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		domain, pattern, ok := strings.Cut(line, ":")
		if !ok {
			domain, pattern, ok = strings.Cut(line, "=")
		}
		if !ok {
			return nil, fmt.Errorf("邮箱规则第 %d 行格式错误，请使用 domain: regex", lineNumber+1)
		}

		domain = strings.ToLower(strings.TrimSpace(domain))
		pattern = strings.TrimSpace(pattern)
		if domain == "" || pattern == "" {
			return nil, fmt.Errorf("邮箱规则第 %d 行域名或正则为空", lineNumber+1)
		}
		if !emailRuleDomainPattern.MatchString(domain) {
			return nil, fmt.Errorf("邮箱规则第 %d 行域名格式错误", lineNumber+1)
		}

		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("邮箱规则第 %d 行正则无效: %w", lineNumber+1, err)
		}
		rules[domain] = compiled
	}
	return rules, nil
}
