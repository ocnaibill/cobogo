package diagnosis

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func variable(name string, hint string) domain.ConfigVariable {
	v := domain.ConfigVariable{Name: name, Class: domain.ClassUser}
	if hint != "" {
		v.Hint = &domain.ConfigHint{Code: hint}
	}
	return v
}

func TestConfirmedWhenTheLogComplainsAboutAnUnsetVariable(t *testing.T) {
	cases := map[string]string{
		"node throw":      "Error: Environment variable DATABASE_URL is not defined\n    at config.js:4",
		"required":        "FATAL: DATABASE_URL is required",
		"missing":         "Missing required environment variable DATABASE_URL",
		"undefined value": "TypeError: connection string DATABASE_URL undefined",
		"must be set":     "config error: DATABASE_URL must be set",
		"not set":         "DATABASE_URL not set, aborting",
		"portuguese":      "variável DATABASE_URL não definida",
	}
	for name, log := range cases {
		t.Run(name, func(t *testing.T) {
			d := Diagnose(log, []domain.ConfigVariable{variable("DATABASE_URL", "")}, true)
			if d.Confidence != Confirmed || len(d.Variables) != 1 || d.Variables[0] != "DATABASE_URL" {
				t.Errorf("diagnosis = %+v", d)
			}
			if !strings.Contains(d.Message, "DATABASE_URL") || !strings.Contains(d.Message, "novo deploy") {
				t.Errorf("message = %q", d.Message)
			}
		})
	}
}

func TestConfirmedListsEveryComplainedVariableInOrder(t *testing.T) {
	log := "SESSION_SECRET is required\nDATABASE_URL is required\nREDIS_URL is not set"
	d := Diagnose(log, []domain.ConfigVariable{variable("REDIS_URL", ""), variable("SESSION_SECRET", ""), variable("DATABASE_URL", "")}, true)
	if d.Confidence != Confirmed || strings.Join(d.Variables, ",") != "DATABASE_URL,REDIS_URL,SESSION_SECRET" {
		t.Errorf("diagnosis = %+v", d)
	}
}

// Nunca afirma sem o nome no log: um erro genérico não confirma nada.
func TestAGenericErrorIsOnlyPossible(t *testing.T) {
	log := "Error: Missing required environment variable\n    at start (index.js:1)"
	unset := []domain.ConfigVariable{variable("API_KEY", domain.HintLikelyRequired), variable("DEBUG", domain.HintLikelyOptional)}
	d := Diagnose(log, unset, true)
	if d.Confidence != Possible || strings.Join(d.Variables, ",") != "API_KEY" {
		t.Errorf("diagnosis = %+v", d)
	}
	if strings.Contains(d.Message, "Provável causa") {
		t.Errorf("a possible diagnosis must not sound certain: %q", d.Message)
	}
}

func TestNamedWithoutAComplaintIsPossible(t *testing.T) {
	d := Diagnose("starting with API_KEY loaded from the environment\ncrash: ECONNREFUSED", []domain.ConfigVariable{variable("API_KEY", domain.HintLikelyOptional)}, false)
	if d.Confidence != Possible || d.Variables[0] != "API_KEY" {
		t.Errorf("diagnosis = %+v", d)
	}
}

func TestUnknownWhenNothingPointsAtAVariable(t *testing.T) {
	for name, c := range map[string]struct {
		log    string
		unset  []domain.ConfigVariable
		exited bool
	}{
		"no unset variables":                      {"Error: ECONNREFUSED", nil, true},
		"optional-looking variables only":         {"Error: something broke", []domain.ConfigVariable{variable("DEBUG", domain.HintLikelyOptional), variable("FEATURE", domain.HintCommented)}, true},
		"the container never exited":              {"slow start", []domain.ConfigVariable{variable("API_KEY", domain.HintLikelyRequired)}, false},
		"a name that only shares a prefix/suffix": {"MY_API_KEY_OLD is required", []domain.ConfigVariable{variable("API_KEY", domain.HintLikelyOptional)}, false},
	} {
		t.Run(name, func(t *testing.T) {
			d := Diagnose(c.log, c.unset, c.exited)
			if d.Confidence != Unknown || d.Message != "" || len(d.Variables) != 0 {
				t.Errorf("diagnosis = %+v", d)
			}
		})
	}
}

func TestVariableNamesAreMatchedAsWholeWords(t *testing.T) {
	d := Diagnose("API_KEY_ID is required", []domain.ConfigVariable{variable("API_KEY", domain.HintLikelyOptional)}, true)
	if d.Confidence == Confirmed {
		t.Errorf("API_KEY_ID must not confirm API_KEY: %+v", d)
	}
}

func TestVariableNamesWithRegexCharactersAreSafe(t *testing.T) {
	d := Diagnose("x is required", []domain.ConfigVariable{variable("A.B*C", "")}, true)
	if d.Confidence != Unknown {
		t.Errorf("diagnosis = %+v", d)
	}
}

func TestUnset(t *testing.T) {
	inventory := []domain.ConfigVariable{
		{Name: "PORT", Class: domain.ClassPlatform},
		{Name: "DATABASE_URL", Class: domain.ClassUser},
		{Name: "JWT_SECRET", Class: domain.ClassGenerable},
		{Name: "LOG_LEVEL", Class: domain.ClassUser},
	}
	got := Unset(inventory, map[string]string{"JWT_SECRET": "x"})
	var names []string
	for _, v := range got {
		names = append(names, v.Name)
	}
	if strings.Join(names, ",") != "DATABASE_URL,LOG_LEVEL" {
		t.Errorf("unset = %v: platform variables and the ones the project has must be excluded", names)
	}
}

func TestPlatformPortIgnoredPointsAtThePortField(t *testing.T) {
	d := PlatformPortIgnored(8080, "index.js:4")
	if d.Field != domain.FieldPort || d.Confidence != Possible {
		t.Errorf("diagnosis = %+v, want field port with possible confidence", d)
	}
	for _, want := range []string{"index.js:4", "8080", "novo deploy"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q lacks %q", d.Message, want)
		}
	}
	if d := PlatformPortIgnored(8080, ""); !strings.Contains(d.Message, "O código lê `PORT`,") {
		t.Errorf("without a source the message must still read well: %q", d.Message)
	}
}

func TestProbeCause(t *testing.T) {
	if got := ProbeCause(400, "django"); !strings.Contains(got, "ALLOWED_HOSTS") {
		t.Errorf("django 400: %q", got)
	}
	for _, tc := range []struct {
		status    int
		framework string
	}{{200, "django"}, {404, "django"}, {400, "flask"}, {400, ""}} {
		if got := ProbeCause(tc.status, tc.framework); got != "" {
			t.Errorf("ProbeCause(%d, %q) = %q, want empty", tc.status, tc.framework, got)
		}
	}
}
