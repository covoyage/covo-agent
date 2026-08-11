package agent

import (
	"fmt"

	"github.com/covoyage/covo-agent/internal/agent/recovery"
)

func (ca *CovoAgent) CredentialPool() *CredentialPool {
	return ca.credentialPool
}

func (ca *CovoAgent) GetActiveCredential() *Credential {
	if ca.credentialPool == nil {
		return nil
	}
	return ca.credentialPool.GetActive()
}

func (ca *CovoAgent) RotateCredential(currentID string) *Credential {
	if ca.credentialPool == nil {
		return nil
	}
	return ca.credentialPool.GetActive()
}

func (ca *CovoAgent) ClassifyError(err error, statusCode int) recovery.ClassifiedError {
	return recovery.ClassifyError(err, statusCode, ca.providerName, ca.model)
}

func (ca *CovoAgent) HandleClassifiedError(err error, statusCode int, credentialID string) recovery.ClassifiedError {
	ce := ca.ClassifyError(err, statusCode)

	if ca.credentialPool != nil && credentialID != "" {
		if ce.ShouldRotateCred {
			ca.credentialPool.MarkExhausted(credentialID, fmt.Sprintf("%d", statusCode))
		}
		if ce.IsAuth() && ce.Reason == recovery.FailoverAuthPermanent {
			ca.credentialPool.MarkDead(credentialID, ce.Message)
		}
	}

	return ce
}

func (ca *CovoAgent) RateLimitState() *RateLimitState {
	return ca.rateLimitState
}
