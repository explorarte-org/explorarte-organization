package contextengine

import (
	"context"
	"fmt"
)

type NoopOwnerConstraintProvider struct{}

func (NoopOwnerConstraintProvider) ListApplicable(context.Context, BuildRequest) ([]SourceRecord, error) {
	return nil, nil
}
func (NoopOwnerConstraintProvider) ValidateVersion(context.Context, SourceRecord) error { return nil }

type UnavailableMemoryProvider struct{}

func (UnavailableMemoryProvider) ListApproved(context.Context, BuildRequest) ([]SourceRecord, error) {
	return nil, ErrSourceProviderUnavailable
}
func (UnavailableMemoryProvider) ValidateVersion(context.Context, SourceRecord) error {
	return ErrSourceProviderUnavailable
}

type UnavailableProjectProvider struct{}

func (UnavailableProjectProvider) GetProjectContext(_ context.Context, request BuildRequest) (*SourceRecord, error) {
	if request.ProjectRef == "" {
		return nil, nil
	}
	return nil, fmt.Errorf("project context: %w", ErrSourceProviderUnavailable)
}
func (UnavailableProjectProvider) ValidateVersion(context.Context, SourceRecord) error {
	return ErrSourceProviderUnavailable
}

type UnavailableTaskProvider struct{}

func (UnavailableTaskProvider) GetTaskContext(_ context.Context, request BuildRequest) (*SourceRecord, error) {
	if request.TaskRef == "" {
		return nil, nil
	}
	return nil, fmt.Errorf("task context: %w", ErrSourceProviderUnavailable)
}
func (UnavailableTaskProvider) ValidateVersion(context.Context, SourceRecord) error {
	return ErrSourceProviderUnavailable
}

type UnavailableRAGProvider struct{}

func (UnavailableRAGProvider) ListApprovedEvidence(context.Context, BuildRequest) ([]SourceRecord, error) {
	return nil, ErrSourceProviderUnavailable
}
func (UnavailableRAGProvider) ValidateVersion(context.Context, SourceRecord) error {
	return ErrSourceProviderUnavailable
}
