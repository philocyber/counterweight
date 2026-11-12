// Remote management credentials require HTTPS. Literal loopback HTTP remains
// available for local development and SSH tunnels; DNS names are not resolved.
export class ManagementTransportError extends Error {}

export function managementTransportAllowed(raw: string, base?: string): boolean {
  try {
    const url = new URL(raw, base);
    if (url.username || url.password) return false;
    if (url.protocol === "https:") return true;
    return url.protocol === "http:" && ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
  } catch {
    return false;
  }
}
