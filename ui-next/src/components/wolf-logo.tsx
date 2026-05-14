// Tiny inline wolf head SVG used in the sidebar header + login. Avoids the
// extra static-asset hop for an image that's <2 KB inlined.
export function WolfLogo({ className = "size-7" }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 64 64"
      className={className}
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-label="Wolf"
    >
      <defs>
        <linearGradient id="wolfg" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0%" stopColor="#60a5fa" />
          <stop offset="100%" stopColor="#a78bfa" />
        </linearGradient>
      </defs>
      <path
        d="M12 18 L20 6 L26 14 L32 8 L38 14 L44 6 L52 18 L52 36 L46 48 L40 52 L34 54 L30 54 L24 52 L18 48 L12 36 Z"
        fill="url(#wolfg)"
        opacity="0.95"
      />
      <circle cx="26" cy="28" r="2.6" fill="#0b0b10" />
      <circle cx="38" cy="28" r="2.6" fill="#0b0b10" />
      <path
        d="M28 38 Q32 42 36 38"
        stroke="#0b0b10"
        strokeWidth="1.6"
        strokeLinecap="round"
        fill="none"
      />
    </svg>
  );
}
