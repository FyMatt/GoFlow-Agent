import { escapeHTML, request } from "../api.js";
import { icon } from "../icons.js";
import { currentLanguage, localizedText, t } from "../i18n.js";
import { formatTokenCompact, formatTokenText } from "../token_format.js";

const usageRanges = ["24h", "7d", "30d"];

export async function renderUsage(root) {
  let selectedRange = "7d";
  let disposed = false;
  const renderShell = () => {
    root.innerHTML = `
      <section class="usage-page" aria-labelledby="usagePageTitle">
        <header class="usage-hero panel">
          <div class="usage-hero-copy">
            <span class="usage-hero-icon">${icon("chart-column")}</span>
            <div>
              <h2 id="usagePageTitle">${escapeHTML(t("usage.title"))}</h2>
              <p>${escapeHTML(t("usage.help"))}</p>
            </div>
          </div>
          <div class="usage-range-switch" role="group" aria-label="${escapeHTML(t("usage.rangeLabel"))}">
            ${usageRanges.map(value => `<button type="button" data-usage-range="${value}" class="${value === selectedRange ? "active" : ""}" aria-pressed="${value === selectedRange ? "true" : "false"}">${escapeHTML(t(`usage.range.${value}`))}</button>`).join("")}
          </div>
        </header>
        <div class="usage-loading panel">${escapeHTML(t("common.loading"))}</div>
      </section>`;
    bindRangeButtons();
  };
  const bindRangeButtons = () => {
    root.querySelectorAll("[data-usage-range]").forEach(button => {
      button.addEventListener("click", () => {
        const next = button.dataset.usageRange || "7d";
        if (next === selectedRange) return;
        selectedRange = next;
        load().catch(() => {});
      });
    });
  };
  const load = async () => {
    renderShell();
    try {
      const usage = await request(`/api/usage?range=${encodeURIComponent(selectedRange)}`);
      if (disposed) return;
      renderUsageData(root.querySelector(".usage-page"), usage);
    } catch (error) {
      if (disposed) return;
      const loading = root.querySelector(".usage-loading");
      if (loading) loading.innerHTML = `<strong>${escapeHTML(t("common.errorTitle"))}</strong><span>${escapeHTML(localizedText(error?.message || String(error || "")))}</span>`;
    }
  };
  const dispose = () => { disposed = true; };
  window.addEventListener("goflow:view-dispose", dispose, { once: true });
  await load();
}

function renderUsageData(page, usage = {}) {
  if (!page) return;
  const loading = page.querySelector(".usage-loading");
  loading?.remove();
  page.querySelectorAll("[data-usage-range]").forEach(button => {
    const active = button.dataset.usageRange === usage.range;
    button.classList.toggle("active", active);
    button.setAttribute("aria-pressed", active ? "true" : "false");
  });
  const lifetime = usage.lifetime || {};
  const period = usage.period || {};
  const metrics = [
    ["database", t("usage.metric.lifetime"), formatTokenText(lifetime.total_tokens, t("chat.tokens")), t("usage.metric.lifetimeHelp")],
    ["activity", t("usage.metric.period"), formatTokenText(period.total_tokens, t("chat.tokens")), t("usage.metric.periodHelp")],
    ["arrow-up", t("usage.metric.prompt"), formatTokenText(period.prompt_tokens, t("chat.tokens")), t("usage.metric.promptHelp")],
    ["sparkles", t("usage.metric.output"), formatTokenText(period.output_tokens, t("chat.tokens")), t("usage.metric.outputHelp")],
    ["history", t("usage.metric.cached"), formatTokenText(period.cached_tokens, t("chat.tokens")), `${formatPercent(period.cache_hit_rate)} · ${t("usage.metric.cachedHelp")}`],
    ["bot", t("usage.metric.calls"), formatInteger(period.model_calls), `${formatInteger(period.runs)} ${t("usage.runs")}`]
  ];
  const dashboard = document.createElement("div");
  dashboard.className = "usage-dashboard";
  dashboard.innerHTML = `
    <section class="usage-metrics" aria-label="${escapeHTML(t("usage.metricsTitle"))}">
      ${metrics.map(([iconName, label, value, help]) => `
        <article class="usage-metric panel">
          <span class="usage-metric-icon">${icon(iconName)}</span>
          <span>${escapeHTML(label)}</span>
          <strong>${escapeHTML(value)}</strong>
          <small>${escapeHTML(help)}</small>
        </article>`).join("")}
    </section>
    <section class="usage-chart-grid">
      <article class="usage-chart-card panel usage-chart-wide">
        ${usageChartHeader(t("usage.trendTitle"), t("usage.trendHelp"), [
          ["total", t("usage.legend.total")],
          ["input", t("usage.legend.input")],
          ["cached", t("usage.legend.cached")],
          ["output", t("usage.legend.output")]
        ], "trend")}
        ${renderTrendChart(usage.buckets || [], usage.bucket_unit)}
      </article>
      <article class="usage-chart-card panel usage-chart-wide">
        ${usageChartHeader(t("usage.compositionTitle"), t("usage.compositionHelp"), [
          ["uncached", t("usage.legend.uncached")],
          ["cached", t("usage.legend.cached")],
          ["output", t("usage.legend.output")]
        ], "composition")}
        ${renderCompositionChart(usage.buckets || [], usage.bucket_unit)}
      </article>
      <article class="usage-chart-card panel usage-agent-card">
        ${usageChartHeader(t("usage.agentTitle"), t("usage.agentHelp"), [], "agents")}
        ${renderAgentUsage(usage.agents || [])}
      </article>
      <article class="usage-chart-card panel usage-summary-card">
        ${usageChartHeader(t("usage.summaryTitle"), t("usage.summaryHelp"), [], "summary")}
        ${renderUsageSummary(usage)}
      </article>
    </section>`;
  page.appendChild(dashboard);
  bindUsageLegendToggles(page);
  bindUsageChartTooltips(page, usage.buckets || [], usage.bucket_unit);
}

function usageChartHeader(title, help, legends, chartID) {
  return `<header class="usage-chart-head"><div><h3>${escapeHTML(title)}</h3><p>${escapeHTML(help)}</p></div>${legends.length ? `<div class="usage-legend" data-usage-legend="${escapeHTML(chartID)}">${legends.map(([tone, label]) => `<button type="button" class="${tone}" data-usage-series="${tone}" aria-pressed="true" title="${escapeHTML(t("usage.legendToggle", { series: label }))}"><i></i>${escapeHTML(label)}</button>`).join("")}</div>` : ""}</header>`;
}

function renderTrendChart(buckets, unit) {
  if (!buckets.length || !buckets.some(item => Number(item.total_tokens || 0) > 0)) return usageEmptyChart();
  const width = 960;
  const height = 286;
  const left = 62;
  const right = 24;
  const top = 24;
  const bottom = 46;
  const plotWidth = width - left - right;
  const plotHeight = height - top - bottom;
  const max = Math.max(1, ...buckets.map(item => Number(item.total_tokens || 0)));
  const slot = plotWidth / Math.max(1, buckets.length);
  const point = (value, index) => {
    const x = left + slot * (index + .5);
    const y = top + plotHeight - plotHeight * Number(value || 0) / max;
    return [x, y];
  };
  const totalPoints = buckets.map((item, index) => point(item.total_tokens, index));
  const inputPoints = buckets.map((item, index) => point(item.prompt_tokens, index));
  const cachedPoints = buckets.map((item, index) => point(item.cached_tokens, index));
  const outputPoints = buckets.map((item, index) => point(item.output_tokens, index));
  const line = points => points.map((item, index) => `${index ? "L" : "M"}${item[0].toFixed(1)},${item[1].toFixed(1)}`).join(" ");
  const area = `${line(totalPoints)} L${totalPoints.at(-1)[0].toFixed(1)},${(top + plotHeight).toFixed(1)} L${totalPoints[0][0].toFixed(1)},${(top + plotHeight).toFixed(1)} Z`;
  const ticks = chartTickIndices(buckets.length);
  return `<div class="usage-svg-shell" data-usage-chart="trend"><svg class="usage-chart-svg" viewBox="0 0 ${width} ${height}" role="img" aria-label="${escapeHTML(t("usage.trendTitle"))}">
    <defs><linearGradient id="usageTrendArea" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="currentColor" stop-opacity=".2"/><stop offset="1" stop-color="currentColor" stop-opacity=".02"/></linearGradient></defs>
    ${[0, .25, .5, .75, 1].map(ratio => {
      const y = top + plotHeight * ratio;
      const value = max * (1 - ratio);
      return `<line x1="${left}" y1="${y}" x2="${width - right}" y2="${y}" class="grid"/><text x="${left - 10}" y="${y + 4}" text-anchor="end">${escapeHTML(formatTokenCompact(value))}</text>`;
    }).join("")}
    <path d="${area}" class="usage-area" data-usage-plot-series="total"/>
    <path d="${line(totalPoints)}" class="usage-line total" data-usage-plot-series="total"/>
    <path d="${line(inputPoints)}" class="usage-line input" data-usage-plot-series="input"/>
    <path d="${line(cachedPoints)}" class="usage-line cached" data-usage-plot-series="cached"/>
    <path d="${line(outputPoints)}" class="usage-line output" data-usage-plot-series="output"/>
    ${totalPoints.map((item, index) => `<circle cx="${item[0]}" cy="${item[1]}" r="3.5" class="usage-point" data-usage-plot-series="total"><title>${escapeHTML(bucketTooltip(buckets[index]))}</title></circle>`).join("")}
    <line x1="0" y1="${top}" x2="0" y2="${top + plotHeight}" class="usage-hover-line"/>
    ${totalPoints.map((item, index) => `<rect x="${(left + slot * index).toFixed(1)}" y="${top}" width="${slot.toFixed(1)}" height="${plotHeight}" class="usage-hit-target" data-bucket-index="${index}" data-chart-x="${item[0].toFixed(1)}" tabindex="0" aria-label="${escapeHTML(bucketTooltip(buckets[index]))}"/>`).join("")}
    ${ticks.map(index => `<text x="${totalPoints[index][0]}" y="${height - 17}" text-anchor="middle">${escapeHTML(bucketLabel(buckets[index].start, unit))}</text>`).join("")}
  </svg><div class="usage-chart-tooltip hidden" role="status"></div></div>`;
}

function renderCompositionChart(buckets, unit) {
  if (!buckets.length || !buckets.some(item => Number(item.total_tokens || 0) > 0)) return usageEmptyChart();
  const width = 960;
  const height = 270;
  const left = 54;
  const right = 22;
  const top = 20;
  const bottom = 46;
  const plotHeight = height - top - bottom;
  const plotWidth = width - left - right;
  const max = Math.max(1, ...buckets.map(item => Number(item.total_tokens || 0)));
  const slot = plotWidth / buckets.length;
  const barWidth = Math.max(7, Math.min(buckets.length <= 10 ? 68 : 32, slot * .62));
  const ticks = chartTickIndices(buckets.length);
  return `<div class="usage-svg-shell" data-usage-chart="composition"><svg class="usage-chart-svg" viewBox="0 0 ${width} ${height}" role="img" aria-label="${escapeHTML(t("usage.compositionTitle"))}">
    ${[0, .5, 1].map(ratio => {
      const y = top + plotHeight * ratio;
      return `<line x1="${left}" y1="${y}" x2="${width - right}" y2="${y}" class="grid"/><text x="${left - 9}" y="${y + 4}" text-anchor="end">${escapeHTML(formatTokenCompact(max * (1 - ratio)))}</text>`;
    }).join("")}
    ${buckets.map((item, index) => Number(item.total_tokens || 0) > 0 ? `<rect x="${(left + slot * index + (slot - barWidth) / 2).toFixed(1)}" y="${top}" width="${barWidth.toFixed(1)}" height="${plotHeight}" rx="4" class="usage-bar-slot"/>` : "").join("")}
    ${buckets.map((item, index) => {
      const prompt = Math.max(0, Number(item.prompt_tokens || 0));
      const cached = Math.min(prompt, Math.max(0, Number(item.cached_tokens || 0)));
      const uncached = Math.max(0, prompt - cached);
      const output = Math.max(0, Number(item.output_tokens || 0));
      const x = left + slot * index + (slot - barWidth) / 2;
      let y = top + plotHeight;
      return [["uncached", uncached], ["cached", cached], ["output", output]].map(([tone, value]) => {
        const barHeight = plotHeight * value / max;
        y -= barHeight;
        return `<rect x="${x.toFixed(1)}" y="${y.toFixed(1)}" width="${barWidth.toFixed(1)}" height="${Math.max(0, barHeight).toFixed(1)}" rx="2" class="usage-bar ${tone}" data-usage-plot-series="${tone}"><title>${escapeHTML(bucketTooltip(item))}</title></rect>`;
      }).join("");
    }).join("")}
    <line x1="0" y1="${top}" x2="0" y2="${top + plotHeight}" class="usage-hover-line"/>
    ${buckets.map((item, index) => `<rect x="${(left + slot * index).toFixed(1)}" y="${top}" width="${slot.toFixed(1)}" height="${plotHeight}" class="usage-hit-target" data-bucket-index="${index}" data-chart-x="${(left + slot * index + slot / 2).toFixed(1)}" tabindex="0" aria-label="${escapeHTML(bucketTooltip(item))}"/>`).join("")}
    ${ticks.map(index => `<text x="${(left + slot * index + slot / 2).toFixed(1)}" y="${height - 16}" text-anchor="middle">${escapeHTML(bucketLabel(buckets[index].start, unit))}</text>`).join("")}
  </svg><div class="usage-chart-tooltip hidden" role="status"></div><p class="usage-chart-note">${escapeHTML(t("usage.cacheWriteUnavailable"))}</p></div>`;
}

function bindUsageLegendToggles(page) {
  page.querySelectorAll(".usage-legend [data-usage-series]").forEach(button => {
    button.addEventListener("click", () => {
      const active = button.getAttribute("aria-pressed") !== "false";
      button.setAttribute("aria-pressed", active ? "false" : "true");
      button.classList.toggle("inactive", active);
      const card = button.closest(".usage-chart-card");
      const series = button.dataset.usageSeries || "";
      card?.querySelectorAll(`[data-usage-plot-series="${series}"]`).forEach(node => node.classList.toggle("series-hidden", active));
    });
  });
}

function bindUsageChartTooltips(page, buckets, unit) {
  page.querySelectorAll("[data-usage-chart]").forEach(shell => {
    const tooltip = shell.querySelector(".usage-chart-tooltip");
    const hoverLine = shell.querySelector(".usage-hover-line");
    if (!tooltip) return;
    const hide = () => {
      tooltip.classList.add("hidden");
      hoverLine?.classList.remove("active");
    };
    const show = (target, event) => {
      const index = Number(target?.dataset.bucketIndex);
      const bucket = buckets[index];
      if (!bucket) return;
      tooltip.innerHTML = usageTooltipHTML(bucket, unit, activeUsageSeries(shell), shell.dataset.usageChart || "");
      tooltip.classList.remove("hidden");
      if (hoverLine) {
        const x = Number(target.dataset.chartX || 0);
        hoverLine.setAttribute("x1", String(x));
        hoverLine.setAttribute("x2", String(x));
        hoverLine.classList.add("active");
      }
      const shellRect = shell.getBoundingClientRect();
      const targetRect = target.getBoundingClientRect();
      const pointerX = Number(event?.clientX || 0) || targetRect.left + targetRect.width / 2;
      const pointerY = Number(event?.clientY || 0) || targetRect.top + 24;
      const width = tooltip.offsetWidth || 230;
      const left = Math.max(10, Math.min(shellRect.width - width - 10, pointerX - shellRect.left + 12));
      const top = Math.max(10, Math.min(shellRect.height - tooltip.offsetHeight - 10, pointerY - shellRect.top - 18));
      tooltip.style.left = `${left}px`;
      tooltip.style.top = `${top}px`;
    };
    shell.querySelectorAll(".usage-hit-target").forEach(target => {
      target.addEventListener("pointerenter", event => show(target, event));
      target.addEventListener("pointermove", event => show(target, event));
      target.addEventListener("pointerleave", hide);
      target.addEventListener("focus", event => show(target, event));
      target.addEventListener("blur", hide);
    });
  });
}

function activeUsageSeries(shell) {
  const active = new Set();
  shell.closest(".usage-chart-card")?.querySelectorAll(".usage-legend [data-usage-series]").forEach(button => {
    if (button.getAttribute("aria-pressed") !== "false") active.add(button.dataset.usageSeries || "");
  });
  return active;
}

function usageTooltipHTML(bucket, unit, active, chartType) {
  const prompt = Math.max(0, Number(bucket.prompt_tokens || 0));
  const cached = Math.min(prompt, Math.max(0, Number(bucket.cached_tokens || 0)));
  const enabled = key => active?.has(key) === true;
  const rows = [
    ["total", "total", t("usage.tooltip.total"), formatTokenText(bucket.total_tokens, t("chat.tokens")), chartType === "composition" || enabled("total")],
    ["input", "input", t("usage.tooltip.input"), formatTokenText(prompt, t("chat.tokens")), chartType === "composition" || enabled("input")],
    ["uncached", "input", t("usage.tooltip.uncached"), formatTokenText(prompt - cached, t("chat.tokens")), chartType === "composition" ? enabled("uncached") : enabled("input")],
    ["cached", "cached", t("usage.tooltip.cacheHit"), formatTokenText(cached, t("chat.tokens")), enabled("cached")],
    ["cached", "neutral", t("usage.tooltip.cacheWrite"), t("usage.notReported"), enabled("cached")],
    ["output", "output", t("usage.tooltip.output"), formatTokenText(bucket.output_tokens, t("chat.tokens")), enabled("output")],
    ["cached", "cached", t("usage.tooltip.cacheRate"), formatPercent(bucket.cache_hit_rate), enabled("cached")],
    ["neutral", "neutral", t("usage.tooltip.calls"), formatInteger(bucket.model_calls), true]
  ].filter(item => item[4]);
  return `<strong>${escapeHTML(bucketLabel(bucket.start, unit))}</strong><dl>${rows.map(([, tone, label, value]) => `<div><dt><i class="usage-tooltip-dot ${tone}"></i>${escapeHTML(label)}</dt><dd>${escapeHTML(value)}</dd></div>`).join("")}</dl>`;
}

function renderAgentUsage(agents) {
  if (!agents.length) return `<div class="usage-empty">${escapeHTML(t("usage.empty"))}</div>`;
  const max = Math.max(1, ...agents.map(item => Number(item.total_tokens || 0)));
  return `<div class="usage-agent-list">${agents.slice(0, 10).map(item => {
    const percent = Math.max(2, Number(item.total_tokens || 0) / max * 100);
    const label = localizedText(item.agent_id || t("usage.unknownAgent"));
    return `<div class="usage-agent-row"><div><strong>${escapeHTML(label)}</strong><span>${escapeHTML(formatTokenText(item.total_tokens, t("chat.tokens")))}</span></div><div class="usage-agent-track"><i style="width:${percent.toFixed(2)}%"></i></div><small>${escapeHTML(t("usage.agentMeta", { calls: formatInteger(item.model_calls), rate: formatPercent(item.cache_hit_rate) }))}</small></div>`;
  }).join("")}</div>`;
}

function renderUsageSummary(usage) {
  const period = usage.period || {};
  const lifetime = usage.lifetime || {};
  const coverage = usage.coverage || {};
  const rows = [
    [t("usage.summaryLifetime"), formatTokenText(lifetime.total_tokens, t("chat.tokens"))],
    [t("usage.summaryPeriod"), formatTokenText(period.total_tokens, t("chat.tokens"))],
    [t("usage.summaryCalls"), formatInteger(lifetime.model_calls)],
    [t("usage.summaryRuns"), formatInteger(lifetime.runs)],
    [t("usage.summaryCacheRate"), formatPercent(lifetime.cache_hit_rate)],
    [t("usage.summaryCoverage"), coverage.earliest_at ? `${formatUsageDate(coverage.earliest_at)} — ${formatUsageDate(coverage.latest_at)}` : t("common.none")]
  ];
  return `<dl class="usage-summary-list">${rows.map(([label, value]) => `<div><dt>${escapeHTML(label)}</dt><dd>${escapeHTML(value)}</dd></div>`).join("")}</dl><p class="usage-coverage-note">${escapeHTML(t("usage.coverageHelp"))}</p>`;
}

function usageEmptyChart() {
  return `<div class="usage-empty">${escapeHTML(t("usage.empty"))}</div>`;
}

function chartTickIndices(length) {
  if (length <= 1) return [0];
  const count = Math.min(6, length);
  return [...new Set(Array.from({ length: count }, (_, index) => Math.round(index * (length - 1) / (count - 1))))];
}

function bucketLabel(value, unit) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value || "-";
  return new Intl.DateTimeFormat(currentLanguage() === "zh" ? "zh-CN" : undefined, unit === "hour" ? { hour: "2-digit", minute: "2-digit" } : { month: "2-digit", day: "2-digit" }).format(date);
}

function bucketTooltip(bucket = {}) {
  return `${formatUsageDate(bucket.start)} · ${t("usage.legend.total")} ${formatTokenCompact(bucket.total_tokens)} · ${t("usage.legend.cached")} ${formatTokenCompact(bucket.cached_tokens)}`;
}

function formatUsageDate(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value || "-";
  return new Intl.DateTimeFormat(currentLanguage() === "zh" ? "zh-CN" : undefined, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(date);
}

function formatPercent(value) {
  const number = Number(value || 0);
  if (!Number.isFinite(number) || number <= 0) return "0%";
  return `${(Math.min(1, number) * 100).toFixed(1).replace(/\.0$/, "")}%`;
}

function formatInteger(value) {
  const number = Number(value || 0);
  return Number.isFinite(number) ? new Intl.NumberFormat().format(number) : "0";
}
