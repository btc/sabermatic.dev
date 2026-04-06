/** Map a 1-5 score to an HSL color string for inline styles. */
export function scoreColor(score: number): string {
  if (score >= 4) return "hsl(142 71% 45%)";  // green
  if (score >= 3) return "hsl(48 96% 53%)";   // yellow/amber
  if (score >= 2) return "hsl(25 95% 53%)";   // orange
  return "hsl(0 84% 60%)";                     // red
}
