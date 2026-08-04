# ADR-0004: Células independientes y frontera clínica

Estado: propuesto para aprobación en Rama 0.

## Decisión

ClínicaOnline no forma parte del proceso del kernel. Mantiene runtime, base de datos, credenciales y despliegue propios. La organización no recibe acceso directo a datos clínicos ni a la base de la célula. Los cambios de código siguen un flujo de repositorio, revisión y despliegue controlado por la célula.
