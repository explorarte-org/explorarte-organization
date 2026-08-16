#!/usr/bin/env python3
"""Offline adversarial tests for Q3_002_FULL_ENTRY_GATE."""

import importlib.util
from pathlib import Path
import unittest


MODULE_PATH = Path(__file__).with_name("q3_002_full_entry_gate.py")
SPEC = importlib.util.spec_from_file_location("q3_002_full_entry_gate", MODULE_PATH)
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)


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


def binding_fixture():
    repair = {key: f"hash-{key}" for key in gate.REPAIR_ARTIFACTS}
    return {
        "schema_version": gate.SCHEMA,
        "mission_id": "ORGANIZATION-REDESIGN-001",
        "question_id": gate.QUESTION_ID,
        "binding_role": "SCIENTIFIC_MEASUREMENT_CANDIDATE",
        "declared_base_sha": gate.BASE_SHA,
        "remote_main_sha_at_freeze": gate.BASE_SHA,
        "measurement_roots": {name: "/frozen" for name in (
            "measurement_root", "tool_root", "citation_root", "integrity_root"
        )},
        "active_questions": [gate.QUESTION_ID],
        "initial_epistemic_state": "UNKNOWN",
        "initial_state": {
            "artifact": "/state",
            "sha256": "state",
            "construction_method": "CONSTRUCTED_FROM_SCHEMA_WHITELIST",
            "historical_source": None,
            "historical_state_subtraction": False,
        },
        "controller": {
            "instrument": "INSTRUMENT_V4",
            "path": "/controller",
            "sha256": gate.CONTROLLER_SHA256,
            "bytes": gate.CONTROLLER_BYTES,
        },
        "question_identity": {
            "contract_sha256": gate.CONTRACT_SHA256,
            "binding_schema": "INSTRUMENT_V4_CONTROLLER_BINDING_V1",
            "source_set_sha256": gate.SOURCE_SET_SHA256,
            "gate_binary_path": "/gate",
            "gate_binary_sha256": gate.GATE_SHA256,
            "gate_binary_bytes": gate.GATE_BYTES,
        },
        "ontology": {
            "version": "Q3_ONTOLOGY_V1",
            "artifact": "ontology.md",
            "whole_file_sha256": gate.ONTOLOGY_WHOLE_SHA256,
            "normative_sha256": gate.ONTOLOGY_NORMATIVE_SHA256,
            "normative_start_offset": 0,
            "normative_end_offset_exclusive": 4,
            "normative_bytes": 4,
        },
        "repair_protection": repair,
        "execution_constraints": {"x": False},
    }


class FullEntryGateTests(unittest.TestCase):
    def setUp(self):
        self.old_source = gate.source_set_sha256
        self.old_read_text = Path.read_text
        self.old_whole = gate.ONTOLOGY_WHOLE_SHA256
        self.old_normative = gate.ONTOLOGY_NORMATIVE_SHA256
        gate.source_set_sha256 = lambda _root: gate.SOURCE_SET_SHA256
        gate.ONTOLOGY_WHOLE_SHA256 = gate.sha256_bytes(b"ontology")
        gate.ONTOLOGY_NORMATIVE_SHA256 = gate.sha256_bytes(b"onto")
        Path.read_text = lambda _path, encoding=None: (
            f'const CanonicalContractSHA256 = "{gate.CONTRACT_SHA256}"'
        )

    def tearDown(self):
        gate.source_set_sha256 = self.old_source
        Path.read_text = self.old_read_text
        gate.ONTOLOGY_WHOLE_SHA256 = self.old_whole
        gate.ONTOLOGY_NORMATIVE_SHA256 = self.old_normative

    @staticmethod
    def artifacts(path):
        text = str(path)
        if text == "/state":
            return {"sha256": "state", "bytes": 1, "raw": b"s"}
        if text == "/controller":
            return {"sha256": gate.CONTROLLER_SHA256, "bytes": gate.CONTROLLER_BYTES, "raw": b"c"}
        if text == "/gate":
            return {"sha256": gate.GATE_SHA256, "bytes": gate.GATE_BYTES, "raw": b"g"}
        if text.endswith("ontology.md"):
            return {"sha256": gate.sha256_bytes(b"ontology"), "bytes": 8, "raw": b"ontology"}
        for key, relative in gate.REPAIR_ARTIFACTS.items():
            if text.endswith(relative):
                return {"sha256": f"hash-{key}", "bytes": 1, "raw": b"r"}
        raise AssertionError(f"unexpected artifact {text}")

    def validate(self, binding, state, head=None, remote=None):
        return gate.validate_static(
            binding,
            state,
            probe=lambda _path: {
                "resolved_path": "/frozen",
                "head": head or gate.BASE_SHA,
                "clean": True,
            },
            file_probe=self.artifacts,
            observed_remote_main_sha=remote,
        )

    def test_remote_main_is_informational_after_scientific_freeze(self):
        outcome = self.validate(binding_fixture(), state_fixture(), remote="f" * 40)
        self.assertEqual(outcome["status"], "Q3_002_STATIC_BINDING_PASS")
        self.assertFalse(outcome["remote_main_is_measurement_authority"])

    def test_previous_repair_base_cannot_replace_scientific_base(self):
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(
                binding_fixture(), state_fixture(),
                head="37ed48d97cfda9efaf4f660a4f626745d793107e",
            )
        self.assertEqual(caught.exception.code, "CAMPAIGN_BASE_SHA_MISMATCH")

    def test_historical_questions_fail_closed(self):
        stale = state_fixture()
        stale["active_questions"] = ["FALSIFY-Q1"]
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(binding_fixture(), stale)
        self.assertEqual(caught.exception.code, "STALE_CAMPAIGN_STATE_DETECTED")

    def test_unknown_state_field_fails_closed(self):
        stale = state_fixture()
        stale["historical_counter"] = 0
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(binding_fixture(), stale)
        self.assertEqual(caught.exception.code, "INITIAL_STATE_SCHEMA_MISMATCH")

    def test_missing_merged_repair_fails_closed(self):
        binding = binding_fixture()
        binding["repair_protection"]["entry_gate_sha256"] = "wrong"
        with self.assertRaises(gate.GateFailure) as caught:
            self.validate(binding, state_fixture())
        self.assertEqual(caught.exception.code, "BINDING_REPAIR_NOT_PRESENT")


if __name__ == "__main__":
    unittest.main()
