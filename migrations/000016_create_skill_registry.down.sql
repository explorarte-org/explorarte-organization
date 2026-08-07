DROP TRIGGER IF EXISTS skill_registry_assignments_no_delete ON skill_registry_assignments;
DROP TRIGGER IF EXISTS skill_registry_assignment_update_guard ON skill_registry_assignments;
DROP TRIGGER IF EXISTS skill_registry_assignment_insert_guard ON skill_registry_assignments;
DROP TRIGGER IF EXISTS skill_registry_versions_no_delete ON skill_registry_versions;
DROP TRIGGER IF EXISTS skill_registry_version_update_guard ON skill_registry_versions;
DROP TRIGGER IF EXISTS skill_registry_version_insert_guard ON skill_registry_versions;
DROP TRIGGER IF EXISTS skill_registry_assignment_idempotency_immutable ON skill_registry_assignment_idempotency;
DROP TRIGGER IF EXISTS skill_registry_skill_idempotency_immutable ON skill_registry_skill_idempotency;
DROP TRIGGER IF EXISTS skill_registry_assignment_events_immutable ON skill_registry_assignment_events;
DROP TRIGGER IF EXISTS skill_registry_lifecycle_events_immutable ON skill_registry_lifecycle_events;
DROP TRIGGER IF EXISTS skill_registry_skills_immutable ON skill_registry_skills;

DROP FUNCTION IF EXISTS skill_registry_reject_assignment_delete();
DROP FUNCTION IF EXISTS skill_registry_guard_assignment_update();
DROP FUNCTION IF EXISTS skill_registry_guard_assignment_insert();
DROP FUNCTION IF EXISTS skill_registry_reject_version_delete();
DROP FUNCTION IF EXISTS skill_registry_guard_version_update();
DROP FUNCTION IF EXISTS skill_registry_guard_version_insert();
DROP FUNCTION IF EXISTS skill_registry_reject_mutation();

DROP TABLE IF EXISTS skill_registry_assignment_idempotency;
DROP TABLE IF EXISTS skill_registry_skill_idempotency;
DROP TABLE IF EXISTS skill_registry_assignment_events;
DROP TABLE IF EXISTS skill_registry_assignments;
DROP TABLE IF EXISTS skill_registry_lifecycle_events;
DROP TABLE IF EXISTS skill_registry_versions;
DROP TABLE IF EXISTS skill_registry_skills;
