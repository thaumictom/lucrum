import type { Config } from "./config";
import { applySetMappings } from "./item-sets";
import type { Dictionary, DictionaryItem } from "./models";
import { isOptionalU32, isRecord } from "./models";
import { readJson, writeJson } from "./storage";
import { fetchItems } from "./warframe-market";

export interface DictionaryResult {
  dictionary: Dictionary;
  source: "api" | "cache";
  setMappingsChanged: boolean;
  mappedComponents: number;
}

export async function getDictionary(config: Config): Promise<DictionaryResult> {
  const cached = await readFreshDictionary(config);
  const source = cached ? "cache" : "api";
  const dictionary = cached ?? (await fetchDictionary(config));
  const mappings = applySetMappings(dictionary.tradeable_items);
  dictionary.tradeable_items = mappings.items;

  if (source === "api" || mappings.changed) {
    await writeJson(config.dictionaryPath, dictionary);
  }

  return {
    dictionary,
    source,
    setMappingsChanged: mappings.changed,
    mappedComponents: mappings.mappedComponents,
  };
}

async function fetchDictionary(config: Config): Promise<Dictionary> {
  const items = await fetchItems(config.requestTimeoutMs);
  return {
    last_fetched_at: new Date().toISOString(),
    tradeable_items: items.map((item) => {
      const result: DictionaryItem = {
        slug: item.slug,
        name: item.i18n?.en?.name ?? item.slug,
        tags: item.tags ?? [],
      };
      if (item.maxRank != null) result.maxRank = item.maxRank;
      if (item.vaulted != null) result.vaulted = item.vaulted;
      if (item.ducats != null) result.ducats = item.ducats;
      return result;
    }),
  };
}

async function readFreshDictionary(
  config: Config,
): Promise<Dictionary | undefined> {
  try {
    const dictionary = parseDictionary(await readJson(config.dictionaryPath));
    const fetchedAt = Date.parse(dictionary.last_fetched_at);
    if (
      !Number.isNaN(fetchedAt) &&
      Date.now() - fetchedAt < config.dictionaryMaxAgeMs
    ) {
      return dictionary;
    }
  } catch {
    // Missing, unreadable, and malformed dictionaries are all refreshed.
  }
  return undefined;
}

function parseDictionary(value: unknown): Dictionary {
  if (
    !isRecord(value) ||
    typeof value.last_fetched_at !== "string" ||
    !Array.isArray(value.tradeable_items)
  ) {
    throw new Error("dictionary has an invalid shape");
  }

  for (const item of value.tradeable_items) {
    if (
      !isRecord(item) ||
      typeof item.slug !== "string" ||
      typeof item.name !== "string" ||
      !Array.isArray(item.tags) ||
      !item.tags.every((tag) => typeof tag === "string") ||
      (item.set_slug !== undefined && typeof item.set_slug !== "string") ||
      !isOptionalU32(item.maxRank) ||
      !isOptionalU32(item.ducats) ||
      (item.vaulted !== undefined &&
        item.vaulted !== null &&
        typeof item.vaulted !== "boolean")
    ) {
      throw new Error("dictionary item has an invalid shape");
    }
  }
  return value as unknown as Dictionary;
}
