package domain

import "testing"

var allStates = []DeploymentState{
	DeploymentQueued, DeploymentFetching, DeploymentAnalyzing, DeploymentNeedsInput, DeploymentUnsupported,
	DeploymentGenerating, DeploymentBuilding, DeploymentProvisioning, DeploymentStarting, DeploymentVerifying,
	DeploymentSucceeded, DeploymentFailed, DeploymentCancelled,
}

func TestHappyPathFollowsTheStateMachine(t *testing.T) {
	path := []DeploymentState{
		DeploymentQueued, DeploymentFetching, DeploymentAnalyzing, DeploymentGenerating, DeploymentBuilding,
		DeploymentProvisioning, DeploymentStarting, DeploymentVerifying, DeploymentSucceeded,
	}
	for i := 0; i+1 < len(path); i++ {
		if !CanTransition(path[i], path[i+1]) {
			t.Errorf("%s -> %s must be allowed", path[i], path[i+1])
		}
	}
}

func TestAnalysisOutcomes(t *testing.T) {
	for _, to := range []DeploymentState{DeploymentNeedsInput, DeploymentUnsupported, DeploymentFailed} {
		if !CanTransition(DeploymentAnalyzing, to) {
			t.Errorf("ANALYZING -> %s must be allowed", to)
		}
	}
	// A resposta do usuário reenfileira; só ela tira o deploy de NEEDS_INPUT.
	if !CanTransition(DeploymentNeedsInput, DeploymentQueued) || !CanTransition(DeploymentNeedsInput, DeploymentCancelled) {
		t.Error("NEEDS_INPUT must go to QUEUED (answer) or CANCELLED")
	}
	if CanTransition(DeploymentNeedsInput, DeploymentFetching) {
		t.Error("NEEDS_INPUT must not resume by itself: only an answer reopens it")
	}
}

func TestEveryActiveStateCanFail(t *testing.T) {
	for _, s := range allStates {
		if s.IsActive() && !CanTransition(s, DeploymentFailed) {
			t.Errorf("%s -> FAILED must be allowed", s)
		}
	}
}

func TestTerminalStatesNeverChange(t *testing.T) {
	for _, from := range allStates {
		if !from.IsTerminal() {
			continue
		}
		for _, to := range allStates {
			if CanTransition(from, to) {
				t.Errorf("terminal %s -> %s must be refused", from, to)
			}
		}
	}
}

func TestResumeRestartsFromFetchingOnlyFromActiveStates(t *testing.T) {
	for _, s := range allStates {
		want := s.IsActive()
		if got := CanTransition(s, DeploymentFetching); got != want && s != DeploymentQueued {
			t.Errorf("%s -> FETCHING = %v, want %v", s, got, want)
		}
	}
	if !CanTransition(DeploymentQueued, DeploymentFetching) {
		t.Error("QUEUED -> FETCHING is the normal start")
	}
}

func TestNoSkippingAhead(t *testing.T) {
	for _, c := range [][2]DeploymentState{
		{DeploymentQueued, DeploymentBuilding},
		{DeploymentFetching, DeploymentBuilding},
		{DeploymentAnalyzing, DeploymentStarting},
		{DeploymentBuilding, DeploymentSucceeded},
		{DeploymentVerifying, DeploymentQueued},
		{DeploymentGenerating, DeploymentNeedsInput},
	} {
		if CanTransition(c[0], c[1]) {
			t.Errorf("%s -> %s must be refused", c[0], c[1])
		}
	}
}

func TestEveryStateIsClassified(t *testing.T) {
	for _, s := range allStates {
		kinds := 0
		for _, b := range []bool{s.IsTerminal(), s.IsActive(), s == DeploymentQueued || s == DeploymentNeedsInput} {
			if b {
				kinds++
			}
		}
		if kinds != 1 {
			t.Errorf("%s must be exactly one of terminal, active, or waiting; got %d", s, kinds)
		}
	}
}
