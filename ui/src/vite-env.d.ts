/// <reference types="vite/client" />

// Wolf-specific env we read in src/lib/api.ts and elsewhere.
interface ImportMetaEnv {
  readonly VITE_API_URL?: string;
  readonly VITE_SCANNER_EVIDENCE_HOSTS?: string;
}
interface ImportMeta {
  readonly env: ImportMetaEnv;
}
