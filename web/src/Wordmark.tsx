// Логотип EVA: E в градиенте, V и A цвета текста, слева лёгкий шлейф.
// Взят из клиента VPN4Eva (gui/src/components/Wordmark.tsx), вручную не править.
export function Wordmark({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="-204 -52 364 104" role="img" aria-label="EVA">
      <defs>
        <linearGradient
          id="eva-g"
          gradientUnits="userSpaceOnUse"
          x1="-147.2"
          y1="0"
          x2="147.2"
          y2="0"
        >
          <stop offset="0" stopColor="#2bd49a" />
          <stop offset="1" stopColor="#3aa8f5" />
        </linearGradient>
        <linearGradient
          id="eva-s"
          gradientUnits="userSpaceOnUse"
          x1="-191.2"
          y1="0"
          x2="-155.2"
          y2="0"
        >
          <stop offset="0" stopColor="#2bd49a" stopOpacity="0" />
          <stop offset="1" stopColor="#2bd49a" stopOpacity="0.22" />
        </linearGradient>
      </defs>
      <g transform="skewX(-12)">
        <path
          d="M-191.2 -50 -155.2 -50 -155.2 -20 -191.2 -20ZM-191.2 -13.8 -155.2 -13.8 -155.2 13.8 -191.2 13.8ZM-191.2 20 -155.2 20 -155.2 50 -191.2 50Z"
          fill="url(#eva-s)"
        />
        <path
          d="M-147.2 -50 -117.2 -50 -117.2 50 -147.2 50ZM-147.2 -50 -75.2 -50 -75.2 -20 -147.2 -20ZM-147.2 20 -75.2 20 -75.2 50 -147.2 50ZM-147.2 -13.8 -75.2 -13.8 -75.2 13.8 -147.2 13.8Z"
          fill="url(#eva-g)"
        />
        <path
          d="M-53.2 -50 -22.5 -50 -5.2 26.7 12 -50 42.8 -50 20.2 50 -30.8 50ZM51.2 50 82 50 99.2 -26.7 116.5 50 147.2 50 124.8 -50 73.8 -50ZM55.2 32.6 143.3 32.6 137.7 7.4 60.8 7.4Z"
          fill="currentColor"
        />
      </g>
    </svg>
  );
}
