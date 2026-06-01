// Brand mark: a stack of layers = a registry of artifacts. Drawn with
// currentColor so it inherits the theme accent (light and dark) wherever it's
// placed. The favicon (public/favicon.svg) is the same mark on a dark chip.
export default function Logo({ className, style }: { className?: string; style?: React.CSSProperties }) {
  return (
    <svg className={className} style={style} viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
      <path d="M16 5 L26 10.5 L16 16 L6 10.5 Z" fill="currentColor" fillOpacity="0.14" />
      <g stroke="currentColor" strokeWidth="2.1" strokeLinejoin="round" strokeLinecap="round">
        <path d="M16 5 L26 10.5 L16 16 L6 10.5 Z" />
        <path d="M6 16 L16 21.5 L26 16" />
        <path d="M6 21 L16 26.5 L26 21" />
      </g>
    </svg>
  );
}
