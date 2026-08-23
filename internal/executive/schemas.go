package executive

import "encoding/json"

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
//
// Keep them inside that subset. In particular:
//   - single-value enum is used instead of const;
//   - repeated definitions are inlined instead of $ref/$defs.
//
// Model Runtime is the provider-boundary schema authority; Executive must not
// maintain a richer incompatible schema dialect above it.
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
	    "unresolved"
	  ],
	  "properties":{
	    "schema_version":{
	      "type":"string",
	      "enum":["department-plan/v1"]
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
	    }
	  }
	}`)

	workerResultOutputSchema = json.RawMessage(`{
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
	    "summary":{"type":"string"},
	    "evidence_refs":{
	      "type":"array",
	      "items":{"type":"string"}
	    },
	    "evidence":{
	      "type":"array",
	      "items":{
	        "type":"object",
	        "additionalProperties":false,
	        "required":["claim","subject","relation","ref"],
	        "properties":{
	          "claim":{"type":"string"},
	          "subject":{"type":"string"},
	          "relation":{"type":"string","enum":["definition","application","test","context"]},
	          "ref":{"type":"string"}
	        }
	      }
	    }
	  }
	}`)

	departmentReviewOutputSchema = json.RawMessage(`{
	  "type":"object",
	  "additionalProperties":false,
	  "required":[
	    "schema_version",
	    "verdict",
	    "findings",
	    "unsatisfied_criteria",
	    "evidence_refs",
	    "proposed_followup_tasks"
	  ],
	  "properties":{
	    "schema_version":{
	      "type":"string",
	      "enum":["department-review/v1"]
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
