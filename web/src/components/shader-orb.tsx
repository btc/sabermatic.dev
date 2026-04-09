import { lazy, Suspense } from "react";

const ShaderOrbImpl = lazy(() =>
  import("./shader-orb-impl").then((m) => ({ default: m.ShaderOrbImpl }))
);

function CssFallbackOrb({ size = 120 }: { size?: number }) {
  return (
    <div
      className="rounded-full animate-pulse"
      style={{
        width: size,
        height: size,
        background:
          "radial-gradient(circle at 40% 40%, #fef3c7, #f59e0b, #92400e)",
        boxShadow: "0 0 40px 10px rgba(245, 158, 11, 0.2)",
      }}
    />
  );
}

function hasWebGL(): boolean {
  try {
    const canvas = document.createElement("canvas");
    return !!(canvas.getContext("webgl") || canvas.getContext("webgl2"));
  } catch {
    return false;
  }
}

interface ShaderOrbProps {
  size?: number;
}

export function ShaderOrb({ size = 120 }: ShaderOrbProps) {
  if (!hasWebGL()) return <CssFallbackOrb size={size} />;

  return (
    <Suspense fallback={<CssFallbackOrb size={size} />}>
      <ShaderOrbImpl size={size} />
    </Suspense>
  );
}
