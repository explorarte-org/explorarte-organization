package rag

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func ContentHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func (v KnowledgeVersion) ComputeCanonicalHash() (string, error) {
	body := struct {
		SchemaVersion       string               `json:"schema_version"`
		DocumentID          string               `json:"document_id"`
		OrganizationID      string               `json:"organization_id"`
		NamespaceKind       NamespaceKind        `json:"namespace_kind"`
		NamespaceID         string               `json:"namespace_id"`
		Version             int64                `json:"version"`
		Title               string               `json:"title"`
		ContentHash         string               `json:"content_hash"`
		SourceKind          SourceKind           `json:"source_kind"`
		SourceReference     string               `json:"source_reference"`
		SourceRunRef        string               `json:"source_run_ref"`
		EvidenceRefs        []EvidenceRef        `json:"evidence_refs"`
		ProposedBy          string               `json:"proposed_by"`
		Admission           AdmissionAttestation `json:"admission"`
		SupersedesVersionID string               `json:"supersedes_version_id"`
	}{
		SchemaVersion: "approved-knowledge.v1", DocumentID: v.DocumentID, OrganizationID: v.OrganizationID,
		NamespaceKind: v.NamespaceKind, NamespaceID: v.NamespaceID, Version: v.Version, Title: v.Title,
		ContentHash: v.ContentHash, SourceKind: v.SourceKind, SourceReference: v.SourceReference, SourceRunRef: v.SourceRunRef,
		EvidenceRefs: append([]EvidenceRef(nil), v.EvidenceRefs...), ProposedBy: v.ProposedBy, Admission: v.Admission,
		SupersedesVersionID: v.SupersedesVersionID,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("hash knowledge version: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
