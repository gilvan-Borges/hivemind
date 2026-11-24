import { tokenBench } from "../dist/pure.js";

const rows = tokenBench();
console.log("cenário\tantes\tdepois\teconomia");
for (const row of rows) {
  const pct = Math.round((1 - row.after / row.before) * 100);
  console.log(`${row.name}\t${row.before}\t${row.after}\t${pct}%`);
}
