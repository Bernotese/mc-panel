export const SOURCE_REPO = 'https://github.com/Bernotese/mc-panel'

/** The source of a release (its tag); development builds and snapshots link to the repository. */
export function sourceUrl(version?: string): string {
  return version && /^\d+\.\d+\.\d+$/.test(version) ? `${SOURCE_REPO}/tree/v${version}` : SOURCE_REPO
}
