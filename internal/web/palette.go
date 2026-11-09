package web

import (
	"net/http"

	"github.com/api-mittens/looster/internal/app"
)

func paletteTasks(repo Repository, write func(http.ResponseWriter, any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		search := r.URL.Query().Get("search")
		if len(search) > 4096 {
			http.Error(w, "Search too long", http.StatusBadRequest)
			return
		}
		reader, ok := repo.(app.PaletteRepository)
		if !ok {
			http.Error(w, "Palette search unavailable", http.StatusNotImplemented)
			return
		}
		tasks, err := reader.PaletteTasks(r.Context(), search)
		write(w, map[string]any{"tasks": tasks}, err)
	}
}
