package main

import (
	"testing"

	"unified/internal/store"
)

func TestDescribe(t *testing.T) {
	movie := store.Media{Kind: "movie", Title: "Arrival", Year: 2016}
	show := store.Media{Kind: "show", Title: "Severance", Year: 2022}
	for _, c := range []struct {
		m     store.Media
		items []store.Item
		want  string
	}{
		{movie, []store.Item{{}}, "Arrival (2016)"},
		{show, []store.Item{{Season: 2, Episode: 3, Title: "Who Is Alive?"}}, "Severance S02E03 - Who Is Alive?"},
		{show, []store.Item{{Season: 2, Episode: 1}, {Season: 2, Episode: 2}}, "Severance season 2 (2 episodes)"},
		{show, []store.Item{{Season: 1, Episode: 9}, {Season: 2, Episode: 1}}, "Severance (2 episodes)"},
	} {
		if got := describe(c.m, c.items); got != c.want {
			t.Errorf("describe = %q, want %q", got, c.want)
		}
	}
}
