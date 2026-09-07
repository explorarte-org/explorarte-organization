package organization

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (s *Service) HandleCEOMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "No se pudo leer el cuerpo de la solicitud")
		return
	}

	var req CEOMessageRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido en la solicitud de chat")
		return
	}

	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeError(w, http.StatusBadRequest, "Escribe un mensaje.")
		return
	}
	if len(msg) > 8000 {
		writeError(w, http.StatusBadRequest, "El mensaje no puede superar 8.000 caracteres.")
		return
	}

	snap, err := s.GetSnapshot(r.Context())
	if err != nil {
		s.logger.Warn("could not get snapshot for CEO context", "error", err)
	}

	lower := strings.ToLower(msg)
	var response string

	switch {
	case strings.HasPrefix(lower, "/mision"):
		objective := strings.TrimSpace(msg[len("/mision"):])
		if objective == "" {
			response = "Para crear una misión, escribe `/mision` seguido de tu objetivo (por ejemplo: `/mision Auditar servicios y optimizar prompts`). También puedes usar el botón + Nueva misión."
		} else {
			response = fmt.Sprintf("He recibido la directriz para la misión: %q. Por favor confírmala enviándola mediante el botón Nueva misión o pulsa Enviar para que la planifique en el kernel.", objective)
		}

	case containsAny(lower, "estado", "resumen", "como vamos", "cómo vamos", "avance", "progreso", "reporte", "status", "situacion", "situación"):
		usedUSD := float64(snap.Metrics.Cost.ActualMicrousd) / 1e6
		budgetUSD := float64(snap.Metrics.Cost.BudgetMicrousd) / 1e6
		response = fmt.Sprintf(
			"Hola Eduardo. Reporte de situación en %s: actualmente contamos con %d misiones activas y %d objetivos completados (de %d totales). Se han registrado %d episodios en MemoryOS y %d skills durables. El consumo acumulado es de $%0.2f USD frente a un presupuesto total de $%0.2f USD. Todos los departamentos están operativos.",
			snap.Organization.Name,
			snap.Metrics.Missions.Active,
			snap.Metrics.Objectives.Completed,
			snap.Metrics.Objectives.Total,
			snap.Metrics.Learning.Episodes,
			snap.Metrics.Skills.Created,
			usedUSD,
			budgetUSD,
		)

	case containsAny(lower, "skill", "aprendizaje", "aprender", "memoria", "memoryos", "recuerdos"):
		response = fmt.Sprintf(
			"La organización cuenta con %d episodios en MemoryOS y %d skills verificadas en el catálogo canónico (incluyendo Skill Forge durable). Cada tarea completada retroalimenta nuestra base de conocimiento para optimizar futuras asignaciones.",
			snap.Metrics.Learning.Episodes,
			snap.Metrics.Skills.Created,
		)

	case containsAny(lower, "costo", "gasto", "dinero", "caja", "presupuesto", "usd", "finanzas"):
		usedUSD := float64(snap.Metrics.Cost.ActualMicrousd) / 1e6
		budgetUSD := float64(snap.Metrics.Cost.BudgetMicrousd) / 1e6
		response = fmt.Sprintf(
			"En finanzas operativas llevamos $%0.2f USD ejecutados de $%0.2f USD asignados (%0.1f%% de utilización). Los techos de gasto se aplican de forma estricta por microUSD en cada tarea.",
			usedUSD,
			budgetUSD,
			(usedUSD/max(budgetUSD, 1))*100,
		)

	case containsAny(lower, "hola", "buenos dias", "buenas tardes", "buenas noches", "saludos", "que tal", "qué tal"):
		response = fmt.Sprintf(
			"Hola Eduardo, a tu disposición como CEO de %s. La organización está en línea y con sus %d departamentos listos. ¿Cuál es el próximo objetivo o campaña que deseas encomendarnos? (Puedes usar `/mision <objetivo>`).",
			snap.Organization.Name,
			len(snap.Departments),
		)

	default:
		response = fmt.Sprintf(
			"Entendido. Como CEO de %s, he tomado nota de tu mensaje. Para encomendar una campaña formal con ejecución de los departamentos, puedes usar el comando `/mision <objetivo>` o presionar + Nueva misión. Estoy atento a tus órdenes.",
			snap.Organization.Name,
		)
	}

	writeJSON(w, http.StatusOK, CEOMessageResponse{
		Message: response,
	})
}

func containsAny(s string, keywords ...string) bool {
	for _, kw := range keywords {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
