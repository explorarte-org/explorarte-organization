package executive

import (
	"encoding/json"
	"fmt"
)

// These schemas deliberately use only the JSON-Schema subset enforced by
// Model Runtime:
//
//   type
//   required
//   properties
//   items
//   additionalProperties
//   enum
//   description
//   maxLength
//
// Keep them inside that subset. In particular:
//   - single-value enum is used instead of const;
//   - repeated definitions are inlined instead of $ref/$defs.
//
// Model Runtime is the provider-boundary schema authority; Executive must not
// maintain a richer incompatible schema dialect above it.
//
// maxLength is declared-and-rendered only: JSON-Schema maxLength counts code
// points, while the host limit these schemas communicate is UTF-8 bytes
// measured with len(string) by validateRequiredString. The authoritative
// enforcement stays there, and every maxLength below is accompanied by a
// description that states the byte rule in words, because maxLength alone
// does not express the byte contract. Both are built from the same Limits
// value the validator uses, so they cannot drift apart.
//
// NOTE (Bug A): task_class in WorkerTaskProposal is declared as {"type":"string"}
// here because Model Runtime does not support the "pattern" keyword. The actual
// syntactic contract (dotted identifiers, max 100 bytes, no legacy.unspecified)
// is enforced exclusively by ValidTaskClass in the host-side parser
// (validateWorkerTaskShape). Both DepartmentPlan.tasks[] and
// DepartmentReview.proposed_followup_tasks[] embed the identical
// taskOutputSchemaJSON const (see below), guaranteeing structural parity
// across all provider-facing paths. Tests in bug_fixes_test.go verify full
// parity between ValidTaskClass rejection and the parser.

const requirementOutputSchemaJSON = `{
  "type":"object",
  "additionalProperties":false,
  "required":["key","type","description","required"],
  "properties":{
    "key":{"type":"string"},
    "type":{
      "type":"string",
      "enum":["artifact","check","approval","condition","result"]
    },
    "description":{"type":"string"},
    "required":{"type":"boolean"}
  }
}`

// taskOutputSchemaJSON is the complete inline JSON-Schema fragment for a
// WorkerTaskProposal item. It is embedded verbatim into both
// departmentPlanOutputSchema (as tasks[]) and departmentReviewOutputSchema
// (as proposed_followup_tasks[]), guaranteeing that every provider-facing
// path uses the exact same task contract. Changing one changes both.
const taskOutputSchemaJSON = `{
  "type":"object",
  "additionalProperties":false,
  "required":[
    "client_key",
    "assigned_role_id",
    "task_class",
    "title",
    "instructions",
    "acceptance_criteria",
    "dependencies",
    "requirements",
    "priority"
  ],
  "properties":{
    "client_key":{
      "type":"string",
      "description":"A name you invent for this task, unique within this response. Other tasks in this same response refer to it by this exact value."
    },
    "assigned_role_id":{"type":"string"},
    "task_class":{"type":"string"},
    "title":{"type":"string"},
    "instructions":{"type":"string"},
    "acceptance_criteria":{
      "type":"array",
      "items":{"type":"string"}
    },
    "dependencies":{
      "type":"array",
      "description":"Tasks in THIS response that must finish before this one starts, named by their client_key. Only client_key values from this same response are valid here. Do not put identifiers of tasks that already exist, such as task:12345 -- a task that already ran cannot be waited for, and references to existing work belong in evidence_refs instead. Use an empty array when this task can start immediately.",
      "items":{"type":"string"}
    },
    "requirements":{
      "type":"array",
      "items":` + requirementOutputSchemaJSON + `
    },
    "priority":{"type":"integer"}
  }
}`

var (
	executivePlanOutputSchema = json.RawMessage(`{
	  "type":"object",
	  "additionalProperties":false,
	  "required":[
	    "schema_version",
	    "objective",
	    "department_requests",
	    "global_constraints",
	    "success_criteria",
	    "owner_decisions_required"
	  ],
	  "properties":{
	    "schema_version":{
	      "type":"string",
	      "enum":["executive-plan/v1"]
	    },
	    "objective":{"type":"string"},
	    "department_requests":{
	      "type":"array",
	      "items":{
	        "type":"object",
	        "additionalProperties":false,
	        "required":[
	          "unit_id",
	          "objective",
	          "deliverable",
	          "priority",
	          "constraints"
	        ],
	        "properties":{
	          "unit_id":{"type":"string"},
	          "objective":{"type":"string"},
	          "deliverable":{"type":"string"},
	          "priority":{"type":"integer"},
	          "constraints":{
	            "type":"array",
	            "items":{"type":"string"}
	          }
	        }
	      }
	    },
	    "global_constraints":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "success_criteria":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "owner_decisions_required":{
	      "type":"array",
	      "items":{"type":"string"}
	    }
	  }
	}`)

	departmentPlanOutputSchema = json.RawMessage(`{
	  "type":"object",
	  "additionalProperties":false,
	  "required":[
	    "schema_version",
	    "department_id",
	    "tasks",
	    "review_criteria",
	    "unresolved",
	    "revision_ownership"
	  ],
	  "properties":{
	    "schema_version":{
	      "type":"string",
	      "enum":["department-plan/v2"]
	    },
	    "department_id":{"type":"string"},
	    "tasks":{
	      "type":"array",
	      "items":` + taskOutputSchemaJSON + `
	    },
	    "review_criteria":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "unresolved":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "revision_ownership":{
	      "type":"array",
	      "description":"Checkpoint E: every required change listed in the plan instructions MUST appear here exactly once, naming the ONE proposed task (by its client_key) that owns resolving it. Unknown ids, missing ids, and two owners for one id are all refused by the host before any worker is created. Support tasks that resolve no required change own nothing and are still allowed. Empty only when the instructions list no required changes.",
	      "items":{
	        "type":"object",
	        "additionalProperties":false,
	        "required":["required_change_id","owner_client_key"],
	        "properties":{
	          "required_change_id":{"type":"string","description":"The exact id from the instructions, e.g. RC:2:1."},
	          "owner_client_key":{"type":"string","description":"The client_key of exactly one task proposed in this same plan."}
	        }
	      }
	    }
	  }
	}`)
)

// byteLimitedStringSchema renders the property schema for every
// worker-result/v2 field whose value passes through
// validateRequiredString(value, limits.MaxStringBytes, ...) -- including
// each evidence_refs[] element, which validateStrings wraps in that same
// check. It states the
// limit twice on purpose: maxLength is the standard keyword a model may
// treat as a character bound, and the description carries the rule the host
// actually enforces -- non-empty, UTF-8 encoded length in bytes. The number
// comes from the same Limits the validator reads, never from a second
// literal.
func byteLimitedStringSchema(limits Limits) string {
	return fmt.Sprintf(`{
	          "type":"string",
	          "maxLength":%d,
	          "description":"Must be non-empty and its UTF-8 encoded representation must not exceed %d bytes."
	        }`, limits.MaxStringBytes, limits.MaxStringBytes)
}

// WorkerResultOutputSchemaFor builds the provider-facing contract for
// PurposeDepartmentWorker results. It was a const literal until R7: the host
// rejected an artifact whose summary exceeded MaxStringBytes while the model
// had never been told any limit existed. The schema now carries the limit,
// derived from the same Limits instance ParseWorkerResult enforces.
func WorkerResultOutputSchemaFor(limits Limits) json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "additionalProperties":false,
	  "required":[
	    "schema_version",
	    "summary",
	    "evidence_refs"
	  ],
	  "properties":{
	    "schema_version":{
	      "type":"string",
	      "enum":["worker-result/v1","worker-result/v2"]
	    },
	    "summary":` + byteLimitedStringSchema(limits) + `,
	    "evidence_refs":{
	      "type":"array",
	      "items":` + byteLimitedStringSchema(limits) + `
	    },
	    "evidence":{
	      "type":"array",
	      "items":{
	        "type":"object",
	        "additionalProperties":false,
	        "required":["claim","subject","relation","ref"],
	        "properties":{
	          "claim":` + byteLimitedStringSchema(limits) + `,
	          "subject":` + byteLimitedStringSchema(limits) + `,
	          "relation":{"type":"string","enum":["definition","application","test","context"]},
	          "ref":` + byteLimitedStringSchema(limits) + `
	        }
	      }
	    }
	  }
	}`)
}

var (
	departmentReviewOutputSchema = json.RawMessage(`{
	  "type":"object",
	  "additionalProperties":false,
	  "required":[
	    "schema_version",
	    "verdict",
	    "findings",
	    "unsatisfied_criteria",
	    "evidence_refs",
	    "proposed_followup_tasks",
	    "revision_outcomes"
	  ],
	  "properties":{
	    "schema_version":{
	      "type":"string",
	      "enum":["department-review/v2"]
	    },
	    "verdict":{
	      "type":"string",
	      "enum":["accept","needs_replan","blocked","fail"]
	    },
	    "findings":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "unsatisfied_criteria":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "evidence_refs":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "proposed_followup_tasks":{
	      "type":"array",
	      "items":` + taskOutputSchemaJSON + `
	    },
	    "revision_outcomes":{
	      "type":"array",
	      "description":"Checkpoint E: per required change in the ownership table, what the deliverables COLLECTIVELY concluded after comparing them against each other. status=resolved means one canonical resolution exists and no deliverable contradicts it; conflicted means two or more deliverables assert incompatible resolutions; unresolved means no deliverable addressed the change. verdict=accept is refused by the host unless every listed change is resolved. Empty only when the plan carried no revision ownership.",
	      "items":{
	        "type":"object",
	        "additionalProperties":false,
	        "required":["required_change_id","status","canonical_resolution","conflicting_task_refs"],
	        "properties":{
	          "required_change_id":{"type":"string","description":"The exact id from the ownership table, e.g. RC:2:1."},
	          "status":{"type":"string","enum":["resolved","unresolved","conflicted"]},
	          "canonical_resolution":{"type":"string","description":"The single agreed resolution, or the empty string when status is not resolved."},
	          "conflicting_task_refs":{
	            "type":"array",
	            "description":"Task references whose assertions conflict, required non-empty exactly when status is conflicted.",
	            "items":{"type":"string"}
	          }
	        }
	      }
	    }
	  }
	
  }`)

	executiveClosureOutputSchema = json.RawMessage(`{
	  "type":"object",
	  "additionalProperties":false,
	  "required":[
	    "schema_version",
	    "status",
	    "answer_to_owner",
	    "completed_items",
	    "blocked_items",
	    "unresolved_decisions",
	    "evidence_refs"
	  ],
	  "properties":{
	    "schema_version":{
	      "type":"string",
	      "enum":["executive-closure/v1"]
	    },
	    "status":{
	      "type":"string",
	      "enum":["completed","partial","blocked","failed"]
	    },
	    "answer_to_owner":{"type":"string"},
	    "completed_items":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "blocked_items":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "unresolved_decisions":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "evidence_refs":{
	      "type":"array",
	      "items":{"type":"string"}
	    }
	  }
	}`)
)
