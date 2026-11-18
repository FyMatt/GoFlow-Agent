export function formatTokenCompact(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number)) return "-";
  const sign = number < 0 ? "-" : "";
  const absolute = Math.abs(number);
  let scale = 1e3;
  let unit = "K";
  if (absolute >= 1e9) {
    scale = 1e9;
    unit = "B";
  } else if (absolute >= 1e6) {
    scale = 1e6;
    unit = "M";
  }
  const scaled = absolute / scale;
  const digits = scaled < 1 ? 3 : scaled < 10 ? 2 : scaled < 100 ? 1 : 0;
  const fixed = scaled.toFixed(digits);
  const text = fixed.includes(".") ? fixed.replace(/0+$/, "").replace(/\.$/, "") : fixed;
  return `${sign}${text || "0"}${unit}`;
}

export function formatTokenText(value, label, options = {}) {
  const number = Number(value || 0);
  if (!Number.isFinite(number) || number < 0) return "-";
  if (number === 0 && options.allowZero === false) return "-";
  return `${formatTokenCompact(number)}${label ? ` ${label}` : ""}`;
}
