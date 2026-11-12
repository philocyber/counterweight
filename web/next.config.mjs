import { fileURLToPath } from "node:url";

// NEXT_EXPORT=1 next build writes a static web/out directory deployable directly to
// an nginx web root. Development omits it to retain API proxying and hot reload.
const isExport = process.env.NEXT_EXPORT === "1";
// Vercel demo uses mocks throughout, with no backend or API proxy.
const isMock = process.env.NEXT_PUBLIC_MOCK === "1";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Avoid parent lockfiles affecting root inference and generated asset paths.
  turbopack: { root: fileURLToPath(new URL(".", import.meta.url)) },
  reactCompiler: true,
  // Allow development HMR resources from LAN IPs; adjust as needed.
  // Permit IPv4 origins for /_next/* and HMR in development, even as LAN addresses change.
  // Next prohibits a bare wildcard; segmented *.*.*.* matches IPv4 origins.
  allowedDevOrigins: ["*.*.*.*"],
  compiler: {
    removeConsole: process.env.NODE_ENV === "production",
  },
  ...(isExport
    ? {
        // Static export has no Node runtime or image optimization; each route emits route/index.html.
        output: "export",
        images: { unoptimized: true },
        trailingSlash: true,
      }
    : isMock
      ? {
          // Vercel mock demo needs no backend or API proxy.
          images: { unoptimized: true },
        }
      : {
          // Development proxies /api/* to Go, default :8787, overridable with COUNTERWEIGHT_API.
          async rewrites() {
            const backend = process.env.COUNTERWEIGHT_API ?? "http://localhost:8787";
            return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
          },
        }),
};

export default nextConfig;
