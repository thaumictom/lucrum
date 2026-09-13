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
  const slugByName = new Map(
    dictionaryItems.map((item) => [normalizeName(item.name), item.slug]),
  );
  const setSlugs = new Set(
    dictionaryItems
      .filter((item) => item.tags.includes("set") || item.slug.endsWith("_set"))
      .map((item) => item.slug),
  );
  const setByComponentSlug = buildComponentMap(slugByName, setSlugs);
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

function buildComponentMap(
  slugByName: Map<string, string>,
  setSlugs: Set<string>,
): Map<string, string> {
  const result = new Map<string, string>();
  const ambiguousComponents = new Set<string>();

  for (const parent of new Items({ ignoreEnemies: true })) {
    if (!("components" in parent) || !Array.isArray(parent.components))
      continue;

    const setSlug = slugByName.get(normalizeName(`${parent.name} Set`));
    if (!setSlug || !setSlugs.has(setSlug)) continue;

    for (const component of parent.components) {
      if (!component.tradable) continue;

      for (const name of componentMarketNames(parent.name, component.name)) {
        const componentSlug = slugByName.get(normalizeName(name));
        if (componentSlug && componentSlug !== setSlug) {
          if (ambiguousComponents.has(componentSlug)) break;
          const existingSet = result.get(componentSlug);
          if (existingSet && existingSet !== setSlug) {
            result.delete(componentSlug);
            ambiguousComponents.add(componentSlug);
          } else {
            result.set(componentSlug, setSlug);
          }
          break;
        }
      }
    }
  }

  return result;
}

function componentMarketNames(
  parentName: string,
  componentName: string,
): string[] {
  if (normalizeName(componentName) === "blueprint") {
    return [`${parentName} Blueprint`];
  }

  const qualifiedName = normalizeName(componentName).startsWith(
    normalizeName(parentName),
  )
    ? componentName
    : `${parentName} ${componentName}`;

  // Warframe components are blueprints on the market; weapon parts usually are not.
  return [qualifiedName, `${qualifiedName} Blueprint`];
}

function normalizeName(name: string): string {
  return name
    .normalize("NFKD")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, " ")
    .trim();
}
