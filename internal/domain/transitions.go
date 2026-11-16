package domain

import "slices"

// transitions é a máquina de estados do §15.2 do mestre. É a única fonte de
// verdade sobre quais mudanças de estado um deploy aceita (DEC-26).
var transitions = map[DeploymentState][]DeploymentState{
	DeploymentQueued:       {DeploymentFetching, DeploymentCancelled},
	DeploymentFetching:     {DeploymentAnalyzing, DeploymentFailed},
	DeploymentAnalyzing:    {DeploymentNeedsInput, DeploymentUnsupported, DeploymentGenerating, DeploymentFailed},
	DeploymentNeedsInput:   {DeploymentQueued, DeploymentCancelled}, // QUEUED só pela resposta do usuário (RF-08)
	DeploymentGenerating:   {DeploymentBuilding, DeploymentFailed},
	DeploymentBuilding:     {DeploymentProvisioning, DeploymentFailed},
	DeploymentProvisioning: {DeploymentStarting, DeploymentFailed},
	DeploymentStarting:     {DeploymentVerifying, DeploymentFailed},
	DeploymentVerifying:    {DeploymentSucceeded, DeploymentFailed},
}

// IsTerminal diz se o estado encerra o deploy: um novo deploy é a única forma
// de nova execução (§15.2).
func (s DeploymentState) IsTerminal() bool {
	switch s {
	case DeploymentSucceeded, DeploymentFailed, DeploymentUnsupported, DeploymentCancelled:
		return true
	}
	return false
}

// IsActive diz se um worker está (ou estava) executando o deploy.
func (s DeploymentState) IsActive() bool {
	switch s {
	case DeploymentFetching, DeploymentAnalyzing, DeploymentGenerating, DeploymentBuilding,
		DeploymentProvisioning, DeploymentStarting, DeploymentVerifying:
		return true
	}
	return false
}

// CanTransition diz se from pode ir para to. Uma retomada (DEC-26) volta a
// FETCHING a partir de qualquer estado ativo: a tentativa recomeça do início,
// sobre o mesmo SHA.
func CanTransition(from, to DeploymentState) bool {
	if to == DeploymentFetching && from.IsActive() {
		return true
	}
	return slices.Contains(transitions[from], to)
}
