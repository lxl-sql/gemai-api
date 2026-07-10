package common

func GetTrustQuota() int {
	// Production-safe default: disable trust pre-consume bypass.
	//
	// The old hard-coded threshold (10 quota units) allowed high-balance users to
	// skip pre-consume entirely. In multi-instance deployments, concurrent
	// requests could all pass the cached balance check and only fail at final
	// settlement after upstream service had already been delivered.
	//
	// Re-enable only after load testing by setting either:
	//   TRUST_QUOTA=<absolute quota>
	//   TRUST_QUOTA_UNITS=<display quota units, multiplied by QuotaPerUnit>
	trustQuota := GetEnvOrDefault("TRUST_QUOTA", 0)
	if trustQuota > 0 {
		return trustQuota
	}
	trustQuotaUnits := GetEnvOrDefault("TRUST_QUOTA_UNITS", 0)
	if trustQuotaUnits > 0 {
		return int(float64(trustQuotaUnits) * QuotaPerUnit)
	}
	return 0
}
