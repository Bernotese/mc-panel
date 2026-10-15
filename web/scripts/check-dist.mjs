// SEC-42: the production UI (internal/webui/dist, embedded into the panel) must not carry dev leftovers:
// the MSW mock layer or source maps. Runs after every `npm run build` (CI job web, make web-build, releases).
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const TEXT = /\.(?:html|js|mjs|css|json|txt|svg)$/
const MARKERS = [/sourceMappingURL=/, /setupWorker/, /mockServiceWorker/, /\bmsw\b/]

/** Lists the problems in dir; an empty list means the build is clean. */
export function findDevArtifacts(dir) {
  const problems = []
  for (const entry of fs.readdirSync(dir, { recursive: true, withFileTypes: true })) {
    if (!entry.isFile()) continue
    const file = path.relative(dir, path.join(entry.parentPath, entry.name))
    if (entry.name === 'mockServiceWorker.js' || entry.name.endsWith('.map')) {
      problems.push(`${file}: must not be shipped`)
      continue
    }
    if (!TEXT.test(entry.name)) continue
    const text = fs.readFileSync(path.join(dir, file), 'utf8')
    for (const marker of MARKERS) {
      if (marker.test(text)) problems.push(`${file}: contains ${marker}`)
    }
  }
  return problems
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dir = path.resolve(process.argv[2] ?? path.join(import.meta.dirname, '../../internal/webui/dist'))
  const problems = findDevArtifacts(dir)
  if (problems.length > 0) {
    console.error(`check-dist: dev artifacts in ${dir}:\n  ${problems.join('\n  ')}`)
    process.exit(1)
  }
  console.log(`check-dist: ${dir} is clean`)
}
