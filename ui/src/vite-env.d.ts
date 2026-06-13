/// <reference types="vite/client" />

// Wolf-specific env we read in src/lib/api.ts and elsewhere.
interface ImportMetaEnv {
  readonly VITE_API_URL?: string;
}
interface ImportMeta {
  readonly env: ImportMetaEnv;
}
