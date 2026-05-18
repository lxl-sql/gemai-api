package common

import "testing"

func TestValidateEmailPolicyWithLocalPartRules(t *testing.T) {
	oldRules := EmailLocalPartRules
	oldDomainRestrictionEnabled := EmailDomainRestrictionEnabled
	oldAliasRestrictionEnabled := EmailAliasRestrictionEnabled
	oldWhitelist := EmailDomainWhitelist
	defer func() {
		EmailLocalPartRules = oldRules
		EmailDomainRestrictionEnabled = oldDomainRestrictionEnabled
		EmailAliasRestrictionEnabled = oldAliasRestrictionEnabled
		EmailDomainWhitelist = oldWhitelist
	}()

	EmailLocalPartRules = `qq.com: ^[1-9][0-9]{4,11}$`
	EmailDomainRestrictionEnabled = false
	EmailAliasRestrictionEnabled = false
	EmailDomainWhitelist = nil

	email, err := ValidateEmailPolicy(" 12345@QQ.COM ")
	if err != nil {
		t.Fatalf("expected numeric QQ email to pass, got error: %v", err)
	}
	if email != "12345@qq.com" {
		t.Fatalf("expected normalized email, got %q", email)
	}

	if _, err := ValidateEmailPolicy("alias@qq.com"); err == nil {
		t.Fatal("expected non-numeric QQ email to be rejected")
	}

	if _, err := ValidateEmailPolicy("user@icloud.com"); err != nil {
		t.Fatalf("expected unrelated domains to pass when no rule is configured, got error: %v", err)
	}
}

func TestValidateEmailLocalPartRulesConfig(t *testing.T) {
	if err := ValidateEmailLocalPartRulesConfig("qq.com: ^[1-9][0-9]{4,11}$"); err != nil {
		t.Fatalf("expected valid rule config, got error: %v", err)
	}

	if err := ValidateEmailLocalPartRulesConfig("qq.com ^[0-9]+$"); err == nil {
		t.Fatal("expected missing separator to be rejected")
	}

	if err := ValidateEmailLocalPartRulesConfig("qq.com: ["); err == nil {
		t.Fatal("expected invalid regular expression to be rejected")
	}
}
