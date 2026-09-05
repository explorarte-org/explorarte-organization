-- Migration 000067: Drop SkillForge and provider divergence tables

DROP TABLE IF EXISTS skillforge_evaluations CASCADE;
DROP TABLE IF EXISTS skillforge_events CASCADE;
DROP TABLE IF EXISTS skillforge_runs CASCADE;
DROP TABLE IF EXISTS skill_source_materializations CASCADE;
DROP TABLE IF EXISTS skillforge_procedure_needs CASCADE;
DROP TABLE IF EXISTS skill_provider_divergences CASCADE;
