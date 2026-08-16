#!/usr/bin/env python3
"""Offline tests for Q3_002_CAMPAIGN_BINDING_REPAIR_001."""

import copy
import importlib.util
from pathlib import Path
import unittest


MODULE_PATH = Path(__file__).with_name("q3_002_campaign_entry_gate.py")
SPEC = importlib.util.spec_from_file_location("q3_002_campaign_entry_gate", MODULE_PATH)
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)


FROZEN = "37ed48d97cfda9efaf4f660a4f626745d793107e"
CONTRACT = "a" * 64


def campaign_fixture():
    return {
        "schema_version": gate.SCHEMA,
        "mission_id": gate.MISSION_ID,
        "phase_id": gate.PHASE_ID,
        "binding_role": "REPAIR_DEVELOPMENT_AND_VALIDATION_ONLY",
        "question_id": gate.QUESTION_ID,
        "declared_base_sha": FROZEN,
        "remote_main_sha_at_freeze": FROZEN,
        "measurement_roots": {name: "/frozen" for name in (
            "measurement_root", "tool_root", "citation_root", "integrity_root"
        )},
        "active_questions": [gate.QUESTION_ID],
        "initial_epistemic_state": "UNKNOWN",
        "initial_state": {
            "artifact": "/state",
            "construction_method": "CONSTRUCTED_FROM_SCHEMA_WHITELIST",
            "historical_source": None,
            "historical_state_subtraction": False,
            "sha256": "s",
            "allowed_fields": sorted(gate.ALLOWED_INITIAL_STATE_FIELDS),
        },
        "controller": {
            "instrument": gate.INSTRUMENT,
            "path": "/controller",
            "sha256": gate.CONTROLLER_SHA256,
            "bytes": gate.CONTROLLER_BYTES,
        },
        "question_identity": {
            "contract_sha256": gate.QUESTION_IDENTITY_CONTRACT_SHA256,
            "binding_schema": "INSTRUMENT_V4_CONTROLLER_BINDING_V1",
            "source_set_sha256": gate.SOURCE_SET_SHA256,
            "gate_binary_path": "/gate",
            "gate_binary_sha256": gate.GATE_BINARY_SHA256,
            "gate_binary_bytes": gate.GATE_BINARY_BYTES,
        },
        "ontology": {
            "version": "Q3_ONTOLOGY_V1",
            "artifact": "ontology.md",
            "whole_file_sha256": gate.ONTOLOGY_WHOLE_FILE_SHA256,
            "normative_sha256": gate.ONTOLOGY_NORMATIVE_SHA256,
            "normative_start_offset": 0,
            "normative_end_offset_exclusive": 4,
            "normative_bytes": 4,
        },
        "execution_constraints": {
            "rerun_002_state_reuse_allowed": False,
            "rerun_002_budget_reuse_allowed": False,
            "historical_questions_allowed": False,
            "model_calls_during_repair_allowed": False,
            "database_access_during_repair_allowed": False,
            "q3_002_execution_during_repair_allowed": False,
            "final_scientific_base_frozen_before_repair_merge_allowed": False,
        },
    }


def state_fixture():
    return {
        "schema_version": "Q3_002_INITIAL_STATE_V1",
        "run_id": "Q3-002-CANONICAL-MEASUREMENT",
        "question_id": gate.QUESTION_ID,
        "active_questions": [gate.QUESTION_ID],
        "questions": [{"question_id": gate.QUESTION_ID, "status": "UNKNOWN"}],
        "model_calls": {"deepseek": 0, "grok": 0, "luna": 0},
        "model_spend_usd": 0.0,
        "database_accesses": 0,
        "refinement_counts": {},
        "tool_call_counts": {},
        "prior_findings": [],
        "prior_dispositions": [],
    }


class BindingTests(unittest.TestCase):
    def setUp(self):
        self.original_source_probe = gate.source_set_sha256
        self.original_read_text = Path.read_text
        self.original_contract = gate.QUESTION_IDENTITY_CONTRACT_SHA256
        self.original_ontology_whole = gate.ONTOLOGY_WHOLE_FILE_SHA256
        self.original_ontology_normative = gate.ONTOLOGY_NORMATIVE_SHA256
        gate.QUESTION_IDENTITY_CONTRACT_SHA256 = CONTRACT
        gate.ONTOLOGY_WHOLE_FILE_SHA256 = gate.sha256_bytes(b"ontology")
        gate.ONTOLOGY_NORMATIVE_SHA256 = gate.sha256_bytes(b"onto")
        gate.source_set_sha256 = lambda _root: gate.SOURCE_SET_SHA256
        Path.read_text = lambda _path, encoding=None: f'const CanonicalContractSHA256 = "{CONTRACT}"'

    def tearDown(self):
        gate.source_set_sha256 = self.original_source_probe
        Path.read_text = self.original_read_text
        gate.QUESTION_IDENTITY_CONTRACT_SHA256 = self.original_contract
        gate.ONTOLOGY_WHOLE_FILE_SHA256 = self.original_ontology_whole
        gate.ONTOLOGY_NORMATIVE_SHA256 = self.original_ontology_normative

    @staticmethod
    def artifacts(path):
        if str(path) == "/controller":
            return {"sha256": gate.CONTROLLER_SHA256, "bytes": gate.CONTROLLER_BYTES, "raw": b"c"}
        if str(path) == "/gate":
            return {"sha256": gate.GATE_BINARY_SHA256, "bytes": gate.GATE_BINARY_BYTES, "raw": b"gg"}
        if str(path) == "/state":
            return {"sha256": "s", "bytes": 1, "raw": b"s"}
        return {"sha256": gate.sha256_bytes(b"ontology"), "bytes": 8, "raw": b"ontology"}

    def validate(self, campaign, state, head=FROZEN, remote="future-main"):
        return gate.validate_binding(
            campaign,
            state,
            root_probe=lambda _path: {"resolved_path": "/frozen", "head": head, "clean": True},
            artifact_probe=self.artifacts,
            observed_remote_main_sha=remote,
        )

    def test_a_remote_main_is_informational_not_measurement_authority(self):
        outcome = self.validate(campaign_fixture(), state_fixture(), remote="f" * 40)
        self.assertEqual(outcome["status"], "Q3_002_CAMPAIGN_BINDING_VALID")
        self.assertFalse(outcome["remote_main_is_runtime_authority"])
        self.assertEqual(outcome["declared_base_sha"], FROZEN)

    def test_b_rerun_root_cannot_satisfy_q3_002_declared_base(self):
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(campaign_fixture(), state_fixture(), head="52275cadf8bc8270e11509e10a89746e601fdf53")
        self.assertEqual(caught.exception.code, "CAMPAIGN_BASE_SHA_MISMATCH")

    def test_c_historical_rerun_questions_fail_closed(self):
        stale = state_fixture()
        stale["active_questions"] = ["FALSIFY-Q1", "FALSIFY-Q2", "FALSIFY-Q4"]
        stale["questions"] = [{"question_id": "FALSIFY-Q1", "status": "UNKNOWN"}]
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(campaign_fixture(), stale)
        self.assertEqual(caught.exception.code, "STALE_CAMPAIGN_STATE_DETECTED")

    def test_campaign_question_id_is_exact(self):
        stale = campaign_fixture()
        stale["question_id"] = "FALSIFY-Q1"
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(stale, state_fixture())
        self.assertEqual(caught.exception.code, "STALE_CAMPAIGN_BINDING_DETECTED")

    def test_nonzero_counters_fail_closed(self):
        stale = state_fixture()
        stale["model_calls"]["grok"] = 1
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(campaign_fixture(), stale)
        self.assertEqual(caught.exception.code, "STALE_CAMPAIGN_COUNTERS_DETECTED")

    def test_four_roots_must_be_identical(self):
        stale = campaign_fixture()
        stale["measurement_roots"]["tool_root"] = "/other"
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(stale, state_fixture())
        self.assertEqual(caught.exception.code, "ROOT_INVARIANT_FAILURE")

    def test_initial_state_is_an_exact_whitelist_not_a_cleaned_copy(self):
        stale = state_fixture()
        stale["unknown_historical_field"] = "survived subtraction"
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(campaign_fixture(), stale)
        self.assertEqual(caught.exception.code, "INITIAL_STATE_SCHEMA_MISMATCH")


if __name__ == "__main__":
    unittest.main()
