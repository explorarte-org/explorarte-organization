package alibabaclaude

import "encoding/json"

// UnmarshalJSON intentionally accepts provider metadata fields that R21 does
// not consume. Claude Code's --output-format json envelope includes session
// and execution metadata and may add new metadata across pinned upgrades. The
// adapter keeps strict parsing for the fields it relies on while treating the
// rest as non-authoritative provider metadata.
func (r *cliJSONResponse) UnmarshalJSON(data []byte) error {
	type usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
	var envelope struct {
		Result           string          `json:"result"`
		StructuredOutput json.RawMessage `json:"structured_output"`
		Usage            usage           `json:"usage"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	r.Result = envelope.Result
	r.StructuredOutput = append(r.StructuredOutput[:0], envelope.StructuredOutput...)
	r.Usage.InputTokens = envelope.Usage.InputTokens
	r.Usage.OutputTokens = envelope.Usage.OutputTokens
	return nil
}
