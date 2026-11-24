package worker

import (
	"errors"
	"testing"

	"github.com/ocnaibill/cobogo/internal/diagnosis"
	"github.com/ocnaibill/cobogo/internal/domain"
)

// DEC-54 C: a falha do build leva o final do log e, quando o log o permite, a correção sugerida.
func TestBuildFailureCarriesTheEpochDiagnosis(t *testing.T) {
	cause := errors.New("build: exit code 1")
	lines := []string{"#13 1.2 Step", "#13 1.7 Error: error:0308010C:digital envelope routines::unsupported"}
	err := buildFailure(cause, lines, domain.Plan{Profile: domain.ProfileStatic})
	var de *detailedError
	if !errors.As(err, &de) || !errors.Is(err, cause) {
		t.Fatalf("expected a detailedError that still wraps the cause: %v", err)
	}
	if de.detail.Diagnosis.Suggestion == nil || de.detail.Diagnosis.Suggestion.Overrides[domain.FieldBuildLegacyOpenSSL] != "true" || de.detail.LogTail == "" {
		t.Fatalf("detail = %+v", de.detail)
	}

	// Sem assinatura conhecida, a falha segue como era (sem detalhe inventado).
	plain := buildFailure(cause, []string{"npm ERR! code ELIFECYCLE"}, domain.Plan{Profile: domain.ProfileNode})
	if !errors.Is(plain, cause) || errors.As(plain, &de) {
		t.Fatalf("an unknown failure must stay as it was: %v", plain)
	}
	if got := buildFailure(cause, nil, domain.Plan{}); got != cause {
		t.Fatalf("no log, no change: %v", got)
	}
	_ = diagnosis.Unknown
}
