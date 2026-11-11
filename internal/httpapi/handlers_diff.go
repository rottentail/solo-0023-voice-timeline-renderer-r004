package httpapi

import "net/http"

// handleRenderDiff compares two completed renders without modifying either
// one. Not-comparable pairs still return 200 with an explicit reason because
// the comparison itself succeeded; unknown render identifiers remain 404 and
// malformed identifiers remain 422 through the standard error envelope.
func (server *Server) handleRenderDiff(w http.ResponseWriter, request *http.Request) {
	comparison, err := server.service.CompareRenders(
		request.Context(),
		request.PathValue("id"),
		request.PathValue("other_id"),
	)
	if err != nil {
		writeError(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, comparison)
}
