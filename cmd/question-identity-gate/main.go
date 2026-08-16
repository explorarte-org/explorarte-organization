package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Mireuz13/explorarte-organization/internal/questionidentity"
)

const maxCandidateBytes = 1 << 20

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	payload, err := io.ReadAll(io.LimitReader(stdin, maxCandidateBytes+1))
	if err != nil {
		return fmt.Errorf("read refinement candidate: %w", err)
	}
	if len(payload) > maxCandidateBytes {
		return fmt.Errorf("refinement candidate exceeds %d bytes", maxCandidateBytes)
	}
	outcome, err := questionidentity.BindControllerPayload(payload)
	if err != nil {
		return fmt.Errorf("bind controller refinement: %w", err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(outcome); err != nil {
		return fmt.Errorf("encode gate outcome: %w", err)
	}
	return nil
}
