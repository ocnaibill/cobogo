package analyzer

// Pistas sobre o valor de uma variável, expostas para a importação de .env
// (DEC-39). Só o tipo ou o veredito sai daqui: o valor é lido em memória e
// descartado, como na análise de arquivos de exemplo (DEC-24 item 8).

// LooksLikePlaceholder diz se o valor parece texto de preenchimento
// (changeme, <token>, ****).
func LooksLikePlaceholder(value string) bool { return isPlaceholder(value) }

// CredentialKind devolve o tipo de credencial que o valor aparenta ser (por
// exemplo "github_token"), ou "" quando não parece uma.
func CredentialKind(name, value string) string { return credentialKind(name, value) }

// CredentialLabel é o nome legível de um tipo de credencial.
func CredentialLabel(kind string) string {
	if label, ok := exposureLabels[kind]; ok {
		return label
	}
	return "credencial"
}
