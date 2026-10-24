export function compareVersions(current: string, target: string): number | null {
  const parse = (version: string) => {
    const match = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(version);
    return match ? match.slice(1).map(Number) : null;
  };
  const a = parse(current);
  const b = parse(target);
  if (!a || !b) return null;
  for (let i = 0; i < 3; i++) {
    if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1;
  }
  return 0;
}

export function releaseStatus(current: string, latest: string): string {
  const comparison = compareVersions(current, latest);
  if (comparison === null) return 'Version unverified';
  if (comparison < 0) return 'Update available';
  if (comparison > 0) return 'Ahead of stable release';
  return 'Up to date';
}
