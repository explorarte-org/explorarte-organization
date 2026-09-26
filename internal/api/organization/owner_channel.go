package organization

import "net/http"

// This API is read-only. It has no owner authentication, and the web front end that proxies it is
// configured to listen on every interface, so an endpoint here that acted as empresa/human would hand
// the owner's authority to whoever can reach it.
//
// Audit 2026-09-26, findings 1, 2 and 4. POST /api/organization/missions created an owner.goal root
// directly: without the executive_closure_verified requirement the executive worker's discovery
// demands, so it was never planned; announcing a budget whose persistence failure was only logged;
// and outside the only path that launches a campaign (proposal -> Finance review -> owner approval ->
// promotion). POST /api/organization/ceo/messages answered from a keyword switch and never reached
// the CEO. Wiring either to the real services without an owner identity would be worse than both, so
// both refuse, say why, and name the channel that does act as the owner. The snapshot advertises
// neither capability.
const ownerChannelRefusal = "Esta API es de solo lectura: no tiene autenticación del propietario, así que no puede hablar con el CEO " +
	"ni crear campañas en su nombre. Usa el canal del propietario: `orgctl executive chat send` para conversar con el CEO " +
	"(propuesta y revisión financiera) y `orgctl campaign approve` / `orgctl campaign promote` para aprobar y lanzar."

func (s *Service) HandleCEOMessage(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusForbidden, ownerChannelRefusal)
}

func (s *Service) HandleCreateMission(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusForbidden, ownerChannelRefusal)
}
