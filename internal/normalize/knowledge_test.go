package normalize

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const knowledgeFixture = `[
 {"uniqueName":"/a","masterable":true,"name":"Ankyros","patchlogs":[{"uniqueName":"/ignored","changes":"old"}],"wikiAvailable":true,
  "damage":{"impact":9007199254740993,"wikiAvailable":false},"levelStats":[{"stats":["first","second"]}],
  "components":[{"uniqueName":"/plate","name":"Short name","itemCount":2,"description":"embedded"},
                {"uniqueName":"/plate","itemCount":2,"description":"another occurrence"}],
  "abilities":[{"uniqueName":"/ability","name":"Ability","description":"Works","patch_logs":[]}],
  "drops":[{"uniqueName":"/source","location":"A mission","type":"Weapon","chance":0.02,"rotation":"C","rarity":"Rare"}],
  "mystery":{"uniqueName":"/unknown","name":"Unknown association","components":[{"uniqueName":"/unwanted-unknown","itemCount":1}]},
  "friend":{"uniqueName":"/promoted","name":"Promoted"}},
 {"uniqueName":"/z","tradable":true,"name":"Another root","requiredItems":[{"uniqueName":"/promoted","quantity":4,"requiredRank":2}],
  "components":[{"uniqueName":"/plate","itemCount":7}]},
 {"uniqueName":"/plate","name":"Alloy Plate","tradable":false,"masterable":false,"description":"Full description",
  "components":[{"uniqueName":"/ore","itemCount":3}],"drops":[{"uniqueName":"/mine","chance":0.3,"location":"Mine","type":"Alloy Plate"}]},
 {"uniqueName":"/ore","name":"Ore","components":[{"uniqueName":"/plate","itemCount":1}]},
 {"uniqueName":"/source","name":"Source","type":"Mission","description":"Display this","imageName":"source.png",
  "components":[{"uniqueName":"/unwanted-source","itemCount":1}],"rewards":[{"chance":1,"rarity":"Rare","item":{"uniqueName":"/unwanted-reward","name":"Reward"}}]},
 {"uniqueName":"/mine","name":"Mine","components":[{"uniqueName":"/unwanted-mine","itemCount":1}]},
 {"uniqueName":"/promoted","name":"Full promoted item","damage":{"heat":42},"components":[{"uniqueName":"/deep","itemCount":1}]},
 {"uniqueName":"/deep","name":"Deep dependency"},
 {"uniqueName":"/relic","tradable":true,"name":"Relic","rewards":[{"chance":0.25,"rarity":"Rare","item":{"uniqueName":"/reward","name":"Reward","warframeMarket":{"id":"legacy"}}}]},
 {"uniqueName":"/reward","name":"Full reward name","components":[{"uniqueName":"/unwanted-reward-child","itemCount":9}]}
]`

func buildFixture(t *testing.T, fixture string) Knowledge {
	t.Helper()
	b := NewKnowledge(nil)
	if err := b.AddSource("source.json", strings.NewReader(fixture)); err != nil {
		t.Fatal(err)
	}
	items, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func TestControlledTraversalAndContexts(t *testing.T) {
	items := buildFixture(t, knowledgeFixture)
	for _, ref := range []string{"/a", "/z", "/plate", "/ore", "/ability", "/source", "/mine", "/unknown", "/promoted", "/deep", "/relic", "/reward"} {
		if items[ref] == nil {
			t.Errorf("missing %s", ref)
		}
	}
	for ref := range items {
		if strings.Contains(ref, "unwanted") || ref == "/ignored" {
			t.Errorf("association or excluded field expanded: %s", ref)
		}
	}
	components := items["/a"]["components"].([]any)
	if len(components) != 1 {
		t.Fatalf("normalized duplicate components: %#v", components)
	}
	if got := jsonKey(components[0]); got != `{"gameRef":"/plate","quantity":2}` {
		t.Fatal(got)
	}
	if got := jsonKey(items["/z"]["components"]); got != `[{"gameRef":"/plate","quantity":7}]` {
		t.Fatal(got)
	}
	if got := items["/plate"]["description"]; got != "Full description" {
		t.Fatal(got)
	}
	if _, exists := items["/plate"]["quantity"]; exists {
		t.Fatal("relationship quantity leaked into canonical item")
	}
	if got := items["/source"]["type"]; got != "Mission" {
		t.Fatalf("drop type polluted source: %v", got)
	}
	if _, exists := items["/source"]["chance"]; exists {
		t.Fatal("drop chance polluted source")
	}
	if _, exists := items["/source"]["components"]; exists {
		t.Fatal("display-only source copied outgoing dependencies")
	}
	if items["/promoted"]["damage"] == nil || items["/deep"] == nil {
		t.Fatal("display record was not promoted")
	}
	if got := jsonKey(items["/relic"]["rewards"]); got != `[{"chance":0.25,"item":{"gameRef":"/reward"},"rarity":"Rare"}]` {
		t.Fatal(got)
	}
	if got := items["/reward"]["name"]; got != "Full reward name" {
		t.Fatal(got)
	}
	raw, _ := json.Marshal(items)
	for _, forbidden := range []string{"uniqueName", "patchlog", "patch_logs", "wikiAvailable"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("published %s", forbidden)
		}
	}
	if !strings.Contains(string(raw), "9007199254740993") {
		t.Fatal("numeric precision lost")
	}
}

func TestSourceArrivalOrderAndMerging(t *testing.T) {
	a := `[{"uniqueName":"/root","masterable":true,"components":[{"uniqueName":"/part","itemCount":1,"name":"Embedded"}]}]`
	b := `[{"uniqueName":"/part","name":"Standalone","levelStats":[{"stats":["one","two"]}],"extra":{"value":1}}]`
	makeIndex := func(reverse bool) Knowledge {
		builder := NewKnowledge(nil)
		order := []struct{ name, data string }{{"a.json", a}, {"b.json", b}}
		if reverse {
			order[0], order[1] = order[1], order[0]
		}
		for _, source := range order {
			if err := builder.AddSource(source.name, strings.NewReader(source.data)); err != nil {
				t.Fatal(err)
			}
		}
		items, err := builder.Build()
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	first, second := makeIndex(false), makeIndex(true)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("source arrival order changed output")
	}
	if first["/part"]["name"] != "Standalone" || first["/part"]["extra"] == nil {
		t.Fatal(first["/part"])
	}
}

func TestAssociationOnlyIdentityAndStringDependencies(t *testing.T) {
	items := buildFixture(t, `[{"uniqueName":"/root","tradable":true,"components":["/missing-part"],"drops":[{"uniqueName":"/missing-source","type":"Root","chance":1}],"arbitrary":{"uniqueName":"/stub","name":"Stub","extra":{"uniqueName":"/do-not-publish"}}}]`)
	if got := jsonKey(items["/missing-source"]); got != `{"gameRef":"/missing-source"}` {
		t.Fatal(got)
	}
	if items["/missing-part"] == nil || items["/do-not-publish"] != nil {
		t.Fatal(items)
	}
}

func TestInvalidKnowledgeAndInput(t *testing.T) {
	for _, input := range []string{"{}", `[{"name":"No identity"}]`, `[{"uniqueName":"/a"}] garbage`} {
		if err := NewKnowledge(nil).AddSource("bad.json", strings.NewReader(input)); err == nil {
			t.Errorf("accepted %s", input)
		}
	}
	if err := ValidateKnowledge(Knowledge{"/a": {"gameRef": "/a", "components": []any{Document{"gameRef": "/missing"}}}}); err == nil {
		t.Fatal("accepted unresolved reference")
	}
}

func TestWrappedRequiredItemKeepsDependencyContext(t *testing.T) {
	items := buildFixture(t, `[
		{"uniqueName":"/root","tradable":true,"requiredItems":[{"item":{"uniqueName":"/part"},"quantity":3,"requiredRank":2}]},
		{"uniqueName":"/part","name":"Part","components":[{"uniqueName":"/deep","itemCount":1}]},
		{"uniqueName":"/deep","name":"Deep"}
	]`)
	if items["/deep"] == nil {
		t.Fatal("wrapped requirement became an association")
	}
	want := `[{"gameRef":"/part","quantity":3,"requiredRank":2}]`
	if got := jsonKey(items["/root"]["requiredItems"]); got != want {
		t.Fatal(got)
	}
}
