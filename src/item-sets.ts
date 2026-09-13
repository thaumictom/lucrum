import Items from "@wfcd/items";
import type { DictionaryItem } from "./models";

export interface SetMappingResult {
  items: DictionaryItem[];
  changed: boolean;
  mappedComponents: number;
}

export function applySetMappings(
  dictionaryItems: DictionaryItem[],
): SetMappingResult {
  const setByGameRef = uniqueSlugByGameRef(
    dictionaryItems.filter((item) => item.tags.includes("set")),
  );
  const componentByGameRef = uniqueSlugByGameRef(
    dictionaryItems.filter(
      (item) =>
        item.tags.includes("component") || item.tags.includes("blueprint"),
    ),
  );
  const setByComponentSlug = buildComponentMap(
    setByGameRef,
    componentByGameRef,
  );
  let changed = false;
  let mappedComponents = 0;

  const items = dictionaryItems.map((item) => {
    const setSlug = setByComponentSlug.get(item.slug);
    if (setSlug) mappedComponents += 1;
    if (item.set_slug === setSlug) return item;

    changed = true;
    const { set_slug: _previousSetSlug, ...itemWithoutSet } = item;
    return setSlug ? { ...itemWithoutSet, set_slug: setSlug } : itemWithoutSet;
  });

  return { items, changed, mappedComponents };
}

function uniqueSlugByGameRef(items: DictionaryItem[]): Map<string, string> {
  const result = new Map<string, string>();
  const ambiguousGameRefs = new Set<string>();

  for (const item of items) {
    if (!item.gameRef || ambiguousGameRefs.has(item.gameRef)) continue;

    const existingSlug = result.get(item.gameRef);
    if (existingSlug && existingSlug !== item.slug) {
      result.delete(item.gameRef);
      ambiguousGameRefs.add(item.gameRef);
    } else {
      result.set(item.gameRef, item.slug);
    }
  }

  return result;
}

function buildComponentMap(
  setByGameRef: Map<string, string>,
  componentByGameRef: Map<string, string>,
): Map<string, string> {
  const result = new Map<string, string>();
  const ambiguousComponents = new Set<string>();

  for (const parent of new Items({ ignoreEnemies: true })) {
    if (!("components" in parent) || !Array.isArray(parent.components))
      continue;

    const setSlug = setByGameRef.get(parent.uniqueName);
    if (!setSlug) continue;

    for (const component of parent.components) {
      if (!component.tradable) continue;

      const componentSlug = componentByGameRef.get(component.uniqueName);
      if (!componentSlug || componentSlug === setSlug) continue;

      if (ambiguousComponents.has(componentSlug)) continue;
      const existingSet = result.get(componentSlug);
      if (existingSet && existingSet !== setSlug) {
        result.delete(componentSlug);
        ambiguousComponents.add(componentSlug);
      } else {
        result.set(componentSlug, setSlug);
      }
    }
  }

  return result;
}
