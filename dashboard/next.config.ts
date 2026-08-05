import path from "node:path";
import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // Pin the workspace root to this directory — without it, Turbopack
  // searches upward for a lockfile and can land on an unrelated one
  // outside the repo (this app has no npm workspace tying it to the
  // Go modules elsewhere in the monorepo).
  turbopack: {
    root: path.join(__dirname),
  },
};

export default nextConfig;
