package xem

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMapAndNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "424536" {
			w.Write([]byte(`{"result":"failure","data":[],"message":"no show with the tvdb_id 1 found"}`))
			return
		}
		switch r.URL.Path {
		case "/map/all":
			w.Write([]byte(`{"result":"success","data":[{"scene":{"season":2,"episode":1,"absolute":1},"tvdb":{"season":2,"episode":1,"absolute":29}}]}`))
		case "/map/names":
			w.Write([]byte(`{"result":"success","data":{"2":{"jp":["Sousou no Frieren S2"]},"all":{"jp":["Sousou no Frieren"]}}}`))
		}
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, HTTP: srv.Client()}

	maps, err := c.Map(424536)
	if err != nil || len(maps) != 1 || maps[0].TVDB.Absolute != 29 || maps[0].Scene.Season != 2 {
		t.Errorf("Map = %+v, %v", maps, err)
	}
	names, err := c.Names(424536)
	if err != nil || len(names) != 1 || names[0] != "Sousou no Frieren" {
		t.Errorf("Names = %q, %v; want only the name for all seasons", names, err)
	}
	if maps, err := c.Map(1); err != nil || len(maps) != 0 {
		t.Errorf("unknown show: Map = %+v, %v; want nothing", maps, err)
	}
}
