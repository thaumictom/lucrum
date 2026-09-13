import { loadConfig } from "./config";
import { getDictionary } from "./dictionary";
import { buildSnapshotRun } from "./snapshots";
import { writeJson } from "./storage";

async function main(): Promise<void> {
  const config = loadConfig();
  const { dictionary, source, setMappingsChanged, mappedComponents } =
    await getDictionary(config);

  if (source === "api") {
    console.log(
      `Wrote ${dictionary.tradeable_items.length} tradeable items to ${config.dictionaryPath}`,
    );
  } else if (setMappingsChanged) {
    console.log(`Updated set mappings in ${config.dictionaryPath}`);
  } else {
    console.log(`Using fresh dictionary at ${config.dictionaryPath}`);
  }
  console.log(`Mapped ${mappedComponents} components to their sets`);

  const run = await buildSnapshotRun(dictionary, config);
  await writeJson(config.snapshotPath, run);
  console.log(
    `Wrote ${run.tradeable_items.length} item snapshots to ${config.snapshotPath}`,
  );
}

if (import.meta.main) {
  main().catch((error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
