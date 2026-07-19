<template>
  <!-- Skull & crossbones (reject indicator). Skull/bones use currentColor;
       eyes + nose are punched dark so they read on the coloured skull. -->
  <svg viewBox="0 0 24 24" fill="none" aria-hidden="true">
    <g stroke="currentColor" stroke-width="2.4" stroke-linecap="round">
      <line x1="6" y1="15.5" x2="18" y2="22" />
      <line x1="18" y1="15.5" x2="6" y2="22" />
    </g>
    <circle cx="12" cy="8" r="5" fill="currentColor" />
    <rect x="8.6" y="11.3" width="6.8" height="4" rx="1.4" fill="currentColor" />
    <circle cx="10" cy="8" r="1.5" fill="#0a0a0a" />
    <circle cx="14" cy="8" r="1.5" fill="#0a0a0a" />
    <path d="M12 9.7 13 11.4 11 11.4Z" fill="#0a0a0a" />
  </svg>
</template>
