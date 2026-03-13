import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  poweredByHeader: false,
  reactStrictMode: true,
  output: process.env.NEXT_OUTPUT === "standalone" ? "standalone" : undefined,
  env: {
    NEXT_PUBLIC_API_URL: process.env.NEXT_PUBLIC_API_URL || "http://localhost:8778/api",
  },
};

export default nextConfig;
