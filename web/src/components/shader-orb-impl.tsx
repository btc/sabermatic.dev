import { useEffect, useRef } from "react";
import * as THREE from "three";

// ---------------------------------------------------------------------------
// GLSL: 3D Simplex noise (Stefan Gustavson, public domain)
// ---------------------------------------------------------------------------
const SIMPLEX_NOISE_GLSL = /* glsl */ `
vec3 mod289(vec3 x) { return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 mod289(vec4 x) { return x - floor(x * (1.0 / 289.0)) * 289.0; }
vec4 permute(vec4 x) { return mod289(((x * 34.0) + 10.0) * x); }
vec4 taylorInvSqrt(vec4 r) { return 1.79284291400159 - 0.85373472095314 * r; }

float snoise(vec3 v) {
  const vec2 C = vec2(1.0 / 6.0, 1.0 / 3.0);
  const vec4 D = vec4(0.0, 0.5, 1.0, 2.0);

  // First corner
  vec3 i  = floor(v + dot(v, C.yyy));
  vec3 x0 = v - i + dot(i, C.xxx);

  // Other corners
  vec3 g = step(x0.yzx, x0.xyz);
  vec3 l = 1.0 - g;
  vec3 i1 = min(g.xyz, l.zxy);
  vec3 i2 = max(g.xyz, l.zxy);

  vec3 x1 = x0 - i1 + C.xxx;
  vec3 x2 = x0 - i2 + C.yyy;
  vec3 x3 = x0 - D.yyy;

  // Permutations
  i = mod289(i);
  vec4 p = permute(permute(permute(
    i.z + vec4(0.0, i1.z, i2.z, 1.0))
  + i.y + vec4(0.0, i1.y, i2.y, 1.0))
  + i.x + vec4(0.0, i1.x, i2.x, 1.0));

  // Gradients: 7x7 points over a square, mapped onto an octahedron.
  float n_ = 0.142857142857; // 1.0/7.0
  vec3  ns = n_ * D.wyz - D.xzx;

  vec4 j = p - 49.0 * floor(p * ns.z * ns.z);

  vec4 x_ = floor(j * ns.z);
  vec4 y_ = floor(j - 7.0 * x_);

  vec4 x = x_ * ns.x + ns.yyyy;
  vec4 y = y_ * ns.x + ns.yyyy;
  vec4 h = 1.0 - abs(x) - abs(y);

  vec4 b0 = vec4(x.xy, y.xy);
  vec4 b1 = vec4(x.zw, y.zw);

  vec4 s0 = floor(b0) * 2.0 + 1.0;
  vec4 s1 = floor(b1) * 2.0 + 1.0;
  vec4 sh = -step(h, vec4(0.0));

  vec4 a0 = b0.xzyw + s0.xzyw * sh.xxyy;
  vec4 a1 = b1.xzyw + s1.xzyw * sh.zzww;

  vec3 p0 = vec3(a0.xy, h.x);
  vec3 p1 = vec3(a0.zw, h.y);
  vec3 p2 = vec3(a1.xy, h.z);
  vec3 p3 = vec3(a1.zw, h.w);

  // Normalise gradients
  vec4 norm = taylorInvSqrt(vec4(dot(p0,p0), dot(p1,p1), dot(p2,p2), dot(p3,p3)));
  p0 *= norm.x;
  p1 *= norm.y;
  p2 *= norm.z;
  p3 *= norm.w;

  // Mix final noise value
  vec4 m = max(0.5 - vec4(dot(x0,x0), dot(x1,x1), dot(x2,x2), dot(x3,x3)), 0.0);
  m = m * m;
  return 105.0 * dot(m*m, vec4(dot(p0,x0), dot(p1,x1), dot(p2,x2), dot(p3,x3)));
}
`;

// ---------------------------------------------------------------------------
// Vertex shader — noise-driven displacement for organic surface ripple
// ---------------------------------------------------------------------------
const vertexShader = /* glsl */ `
${SIMPLEX_NOISE_GLSL}

uniform float uTime;
uniform float uNoiseScale;
uniform float uDisplacementStrength;

varying vec3 vNormal;
varying vec3 vPosition;
varying float vDisplacement;
varying vec3 vWorldPosition;

void main() {
  // Layer two noise octaves for richer surface detail
  float slow = uTime * 0.5;
  float fast = uTime * 1.0;

  float noise1 = snoise(normal * uNoiseScale + slow);
  float noise2 = snoise(normal * uNoiseScale * 2.0 + fast + 100.0) * 0.5;
  float noise3 = snoise(normal * uNoiseScale * 4.0 + slow * 0.5 + 200.0) * 0.25;

  float displacement = (noise1 + noise2 + noise3) * uDisplacementStrength;
  vDisplacement = displacement;

  vec3 newPosition = position + normal * displacement;
  vNormal = normalize(normalMatrix * normal);
  vPosition = newPosition;
  vWorldPosition = (modelMatrix * vec4(newPosition, 1.0)).xyz;

  gl_Position = projectionMatrix * modelViewMatrix * vec4(newPosition, 1.0);
}
`;

// ---------------------------------------------------------------------------
// Fragment shader — warm palette with Fresnel rim glow
// ---------------------------------------------------------------------------
const fragmentShader = /* glsl */ `
${SIMPLEX_NOISE_GLSL}

uniform float uTime;
uniform vec3 uCameraPosition;

varying vec3 vNormal;
varying vec3 vPosition;
varying float vDisplacement;
varying vec3 vWorldPosition;

void main() {
  // Brand palette
  vec3 amber      = vec3(0.96, 0.62, 0.04);
  vec3 terracotta = vec3(0.80, 0.45, 0.20);
  vec3 cream      = vec3(0.99, 0.96, 0.89);
  vec3 cerulean   = vec3(0.25, 0.58, 0.82);
  vec3 sage       = vec3(0.47, 0.63, 0.45);

  // Slow-drifting noise fields drive colour variation across the surface
  float t = uTime * 0.25;
  float colorNoise1 = snoise(vPosition * 1.5 + t);
  float colorNoise2 = snoise(vPosition * 2.0 - t * 0.7 + 50.0);
  float colorNoise3 = snoise(vPosition * 3.0 + t * 0.4 + 100.0);

  // Base: amber ↔ terracotta blend driven by noise
  float baseMix = smoothstep(-0.4, 0.6, colorNoise1);
  vec3 baseColor = mix(amber, terracotta, baseMix);

  // Fold in cerulean sparingly (~15%)
  float ceruleanMask = smoothstep(0.35, 0.65, colorNoise2) * 0.15;
  baseColor = mix(baseColor, cerulean, ceruleanMask);

  // Fold in sage sparingly (~10%)
  float sageMask = smoothstep(0.3, 0.7, colorNoise3) * 0.10;
  baseColor = mix(baseColor, sage, sageMask);

  // Displacement-driven highlight: peaks get cream highlight
  float highlightMix = smoothstep(0.02, 0.10, vDisplacement) * 0.35;
  baseColor = mix(baseColor, cream, highlightMix);

  // Fresnel rim glow — view-dependent edge luminance
  vec3 viewDir = normalize(uCameraPosition - vWorldPosition);
  float fresnel = 1.0 - max(dot(viewDir, vNormal), 0.0);
  fresnel = pow(fresnel, 3.0);

  // Rim colour drifts between amber and cream
  vec3 rimColor = mix(amber, cream, 0.6 + 0.4 * sin(uTime * 0.4));
  baseColor += rimColor * fresnel * 0.7;

  // Subtle subsurface-style inner glow
  float innerGlow = pow(max(dot(viewDir, vNormal), 0.0), 1.5) * 0.15;
  baseColor += amber * innerGlow;

  gl_FragColor = vec4(baseColor, 1.0);
}
`;

// ---------------------------------------------------------------------------
// Glow sphere shaders — soft outer halo
// ---------------------------------------------------------------------------
const glowVertexShader = /* glsl */ `
varying vec3 vNormal;
varying vec3 vWorldPosition;

void main() {
  vNormal = normalize(normalMatrix * normal);
  vWorldPosition = (modelMatrix * vec4(position, 1.0)).xyz;
  gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
}
`;

const glowFragmentShader = /* glsl */ `
uniform float uTime;
uniform vec3 uCameraPosition;

varying vec3 vNormal;
varying vec3 vWorldPosition;

void main() {
  vec3 amber = vec3(0.96, 0.62, 0.04);
  vec3 cream = vec3(0.99, 0.96, 0.89);

  vec3 viewDir = normalize(uCameraPosition - vWorldPosition);
  float fresnel = 1.0 - max(dot(viewDir, vNormal), 0.0);

  // Soft exponential falloff for halo
  float glow = pow(fresnel, 2.5);

  // Breathing pulse
  float pulse = 0.8 + 0.2 * sin(uTime * 0.8);

  vec3 glowColor = mix(amber, cream, fresnel * 0.5);
  float alpha = glow * 0.5 * pulse;

  gl_FragColor = vec4(glowColor, alpha);
}
`;

// ---------------------------------------------------------------------------
// React component
// ---------------------------------------------------------------------------
interface ShaderOrbImplProps {
  size?: number;
}

export function ShaderOrbImpl({ size = 120 }: ShaderOrbImplProps) {
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const width = size;
    const height = size;
    const dpr = Math.min(window.devicePixelRatio, 2);

    // --- Renderer ---
    const renderer = new THREE.WebGLRenderer({
      antialias: true,
      alpha: true,
    });
    renderer.setSize(width, height);
    renderer.setPixelRatio(dpr);
    renderer.setClearColor(0x000000, 0);
    container.appendChild(renderer.domElement);

    // --- Scene & Camera ---
    const scene = new THREE.Scene();
    const camera = new THREE.PerspectiveCamera(45, 1, 0.1, 100);
    camera.position.set(0, 0, 2.8);
    camera.lookAt(0, 0, 0);

    // --- Main orb ---
    const geometry = new THREE.SphereGeometry(1, 64, 64);
    const material = new THREE.ShaderMaterial({
      vertexShader,
      fragmentShader,
      uniforms: {
        uTime: { value: 0 },
        uNoiseScale: { value: 2.0 },
        uDisplacementStrength: { value: 0.22 },
        uCameraPosition: { value: camera.position.clone() },
      },
    });
    const orb = new THREE.Mesh(geometry, material);
    scene.add(orb);

    // --- Outer glow sphere ---
    const glowGeometry = new THREE.SphereGeometry(1.35, 32, 32);
    const glowMaterial = new THREE.ShaderMaterial({
      vertexShader: glowVertexShader,
      fragmentShader: glowFragmentShader,
      uniforms: {
        uTime: { value: 0 },
        uCameraPosition: { value: camera.position.clone() },
      },
      transparent: true,
      side: THREE.BackSide,
      depthWrite: false,
    });
    const glowMesh = new THREE.Mesh(glowGeometry, glowMaterial);
    scene.add(glowMesh);

    // --- Animation loop ---
    let frameId: number;
    const clock = new THREE.Clock();

    function animate() {
      frameId = requestAnimationFrame(animate);
      const elapsed = clock.getElapsedTime();

      material.uniforms.uTime!.value = elapsed;
      glowMaterial.uniforms.uTime!.value = elapsed;

      // Gentle tumble so the noise pattern drifts visually
      orb.rotation.y = elapsed * 0.1;
      orb.rotation.x = Math.sin(elapsed * 0.15) * 0.2;
      glowMesh.rotation.y = elapsed * 0.1;
      glowMesh.rotation.x = Math.sin(elapsed * 0.15) * 0.2;

      renderer.render(scene, camera);
    }
    animate();

    // --- Cleanup ---
    return () => {
      cancelAnimationFrame(frameId);
      geometry.dispose();
      material.dispose();
      glowGeometry.dispose();
      glowMaterial.dispose();
      renderer.dispose();
      if (container.contains(renderer.domElement)) {
        container.removeChild(renderer.domElement);
      }
    };
  }, [size]);

  return (
    <div
      ref={containerRef}
      style={{
        width: size,
        height: size,
        filter: "drop-shadow(0 0 30px rgba(245, 158, 11, 0.3)) drop-shadow(0 0 60px rgba(245, 158, 11, 0.15))",
      }}
      aria-hidden="true"
    />
  );
}
