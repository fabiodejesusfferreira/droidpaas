package runit

import "strings"

// EncodeEnvValue prepara o conteúdo do arquivo do envdir (runit): só a primeira linha vale, então
// quebras de linha viram NUL (que o chpst converte de volta); arquivo vazio apaga a variável, então
// o valor vazio é gravado como "\n". Espaços/tabs finais são cortados pelo chpst (limitação conhecida).
func EncodeEnvValue(v string) []byte {
	if v == "" {
		return []byte("\n")
	}
	return []byte(strings.ReplaceAll(v, "\n", "\x00"))
}
