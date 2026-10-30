// Regenerates games.json from node-gamedig's game catalog (MIT).
// usage: node tools/gen-games/gen-games.mjs <path-to-node-gamedig>/lib/games.js > games.json
import { pathToFileURL } from 'node:url'
import { resolve } from 'node:path'

const { games } = await import(pathToFileURL(resolve(process.argv[2])).href)
const out = {}
for (const id of Object.keys(games).sort()) {
  const g = games[id]
  const o = g.options || {}
  const entry = { name: g.name, protocol: o.protocol }
  if (o.port) entry.port = o.port
  if (o.port_query) entry.port_query = o.port_query
  if (o.port_query_offset !== undefined) {
    entry.port_query_offset = Array.isArray(o.port_query_offset) ? o.port_query_offset : [o.port_query_offset]
  }
  if (g.extra?.old_id) entry.old_id = g.extra.old_id
  out[id] = entry
}
process.stdout.write(JSON.stringify(out, null, 1) + '\n')
