export const onboardingStorageKey = "goflow.onboarding.v1";

const tourOrder = ["shell", "workspace", "catalog", "workflows", "playground", "approvals", "status"];

const tours = {
  shell: {
    id: "shell",
    titleKey: "tour.group.shell",
    steps: [
      {
        id: "shell-sidebar",
        route: "overview",
        target: '[data-tour-id="shell-sidebar"]',
        titleKey: "tour.shell.sidebar.title",
        bodyKey: "tour.shell.sidebar.body"
      },
      {
        id: "shell-nav-workspace",
        route: "overview",
        target: '[data-tour-id="nav-workspace"]',
        titleKey: "tour.shell.workspace.title",
        bodyKey: "tour.shell.workspace.body"
      },
      {
        id: "shell-nav-workflows",
        route: "overview",
        target: '[data-tour-id="nav-workflows"]',
        titleKey: "tour.shell.workflows.title",
        bodyKey: "tour.shell.workflows.body"
      },
      {
        id: "shell-nav-catalog",
        route: "overview",
        target: '[data-tour-id="nav-catalog"]',
        titleKey: "tour.shell.catalog.title",
        bodyKey: "tour.shell.catalog.body"
      },
      {
        id: "shell-nav-playground",
        route: "overview",
        target: '[data-tour-id="nav-playground"]',
        titleKey: "tour.shell.playground.title",
        bodyKey: "tour.shell.playground.body"
      },
      {
        id: "shell-nav-approvals",
        route: "overview",
        target: '[data-tour-id="nav-approvals"]',
        titleKey: "tour.shell.approvals.title",
        bodyKey: "tour.shell.approvals.body"
      },
      {
        id: "shell-nav-status",
        route: "overview",
        target: '[data-tour-id="nav-status"]',
        titleKey: "tour.shell.status.title",
        bodyKey: "tour.shell.status.body"
      },
      {
        id: "shell-topbar",
        route: "overview",
        target: '[data-tour-id="shell-topbar"]',
        titleKey: "tour.shell.topbar.title",
        bodyKey: "tour.shell.topbar.body"
      }
    ]
  },
  workspace: {
    id: "workspace",
    titleKey: "tour.group.workspace",
    steps: [
      {
        id: "workspace-boundary",
        route: "workspace",
        target: '[data-tour-id="workspace-boundary"]',
        titleKey: "tour.workspace.boundary.title",
        bodyKey: "tour.workspace.boundary.body"
      },
      {
        id: "workspace-confirm",
        route: "workspace",
        target: '[data-tour-id="workspace-confirm"]',
        titleKey: "tour.workspace.confirm.title",
        bodyKey: "tour.workspace.confirm.body"
      },
      {
        id: "workspace-switcher",
        route: "workspace",
        target: '[data-tour-id="workspace-switcher"]',
        titleKey: "tour.workspace.switcher.title",
        bodyKey: "tour.workspace.switcher.body"
      }
    ]
  },
  workflows: {
    id: "workflows",
    titleKey: "tour.group.workflows",
    steps: [
      {
        id: "workflow-palette",
        route: "workflows",
        target: '[data-tour-id="workflow-palette"]',
        titleKey: "tour.workflow.palette.title",
        bodyKey: "tour.workflow.palette.body"
      },
      {
        id: "workflow-canvas",
        route: "workflows",
        target: '[data-tour-id="workflow-canvas"]',
        titleKey: "tour.workflow.canvas.title",
        bodyKey: "tour.workflow.canvas.body"
      },
      {
        id: "workflow-node-agent",
        route: "workflows",
        target: '[data-tour-id="workflow-node-agent"]',
        titleKey: "tour.workflow.node.title",
        bodyKey: "tour.workflow.node.body"
      },
      {
        id: "workflow-connector",
        route: "workflows",
        target: '[data-tour-id="workflow-connector"]',
        titleKey: "tour.workflow.connector.title",
        bodyKey: "tour.workflow.connector.body"
      },
      {
        id: "workflow-inspector",
        route: "workflows",
        target: '[data-tour-id="workflow-inspector"]',
        titleKey: "tour.workflow.inspector.title",
        bodyKey: "tour.workflow.inspector.body"
      },
      {
        id: "workflow-run",
        route: "workflows",
        target: '[data-tour-id="workflow-run-preview"]',
        titleKey: "tour.workflow.run.title",
        bodyKey: "tour.workflow.run.body"
      }
    ]
  },
  catalog: {
    id: "catalog",
    titleKey: "tour.group.catalog",
    steps: [
      {
        id: "catalog-hero",
        route: "catalog",
        target: '[data-tour-id="catalog-hero"]',
        titleKey: "tour.catalog.hero.title",
        bodyKey: "tour.catalog.hero.body"
      },
      {
        id: "catalog-agents",
        route: "catalog",
        target: '[data-tour-id="catalog-agents"]',
        titleKey: "tour.catalog.agents.title",
        bodyKey: "tour.catalog.agents.body"
      },
      {
        id: "catalog-skills",
        route: "catalog",
        target: '[data-tour-id="catalog-skills"]',
        titleKey: "tour.catalog.skills.title",
        bodyKey: "tour.catalog.skills.body"
      },
      {
        id: "catalog-tools",
        route: "catalog",
        target: '[data-tour-id="catalog-tools"]',
        titleKey: "tour.catalog.tools.title",
        bodyKey: "tour.catalog.tools.body"
      }
    ]
  },
  playground: {
    id: "playground",
    titleKey: "tour.group.playground",
    steps: [
      {
        id: "playground-targets",
        route: "playground",
        target: '[data-tour-id="playground-targets"]',
        titleKey: "tour.playground.targets.title",
        bodyKey: "tour.playground.targets.body"
      },
      {
        id: "playground-timeline",
        route: "playground",
        target: '[data-tour-id="playground-timeline"]',
        titleKey: "tour.playground.timeline.title",
        bodyKey: "tour.playground.timeline.body"
      },
      {
        id: "playground-files",
        route: "playground",
        target: '[data-tour-id="playground-files"]',
        titleKey: "tour.playground.files.title",
        bodyKey: "tour.playground.files.body"
      }
    ]
  },
  approvals: {
    id: "approvals",
    titleKey: "tour.group.approvals",
    steps: [
      {
        id: "approvals-queue",
        route: "approvals",
        target: '[data-tour-id="approvals-queue"]',
        titleKey: "tour.approvals.queue.title",
        bodyKey: "tour.approvals.queue.body"
      },
      {
        id: "approvals-log",
        route: "approvals",
        target: '[data-tour-id="approvals-log"]',
        titleKey: "tour.approvals.log.title",
        bodyKey: "tour.approvals.log.body"
      }
    ]
  },
  status: {
    id: "status",
    titleKey: "tour.group.status",
    steps: [
      {
        id: "status-hero",
        route: "status",
        target: '[data-tour-id="status-hero"]',
        titleKey: "tour.status.hero.title",
        bodyKey: "tour.status.hero.body"
      },
      {
        id: "status-health",
        route: "status",
        target: '[data-tour-id="status-health"]',
        titleKey: "tour.status.health.title",
        bodyKey: "tour.status.health.body"
      },
      {
        id: "status-logs",
        route: "status",
        target: '[data-tour-id="status-logs"]',
        titleKey: "tour.status.logs.title",
        bodyKey: "tour.status.logs.body"
      }
    ]
  }
};

const state = loadState();
let deps = null;
let activeTarget = null;
let syncFrame = 0;
let syncShouldAlign = false;
let syncToken = 0;
let activeTourRun = 0;
let targetRetryTimer = 0;
let targetRetryCount = 0;
let lastStepKey = "";
let scrollSyncTimer = 0;

const targetRetryLimit = 16;
const targetRetryDelayMs = 120;

export function setupOnboarding(options) {
  deps = options;
  bindOverlay();
  updateTrigger();
}

export function notifyViewRendered(viewName) {
  if (!deps) return;
  if (state.active && currentStep()?.route === viewName) {
    requestSync(true);
  } else {
    if (!state.active) hideOverlay();
    updateTrigger();
  }
}

export function getOnboardingState() {
  return { ...state };
}

export function startOnboarding(tourId = tourOrder[0], stepIndex = 0) {
  const selectedTourId = isRunnableTour(tourId) ? tourId : tourOrder[0];
  const selectedTour = tours[selectedTourId];
  activeTourRun += 1;
  clearTargetRetry();
  window.clearTimeout(scrollSyncTimer);
  scrollSyncTimer = 0;
  targetRetryCount = 0;
  lastStepKey = "";
  state.active = true;
  state.dismissed = false;
  state.completed = false;
  state.tourId = selectedTourId;
  state.stepIndex = clampStepIndex(selectedTour, stepIndex);
  persist();
  updateTrigger();
  routeToCurrentStep();
}

export function resumeOnboarding() {
  if (state.completed) {
    startOnboarding(tourOrder[0], 0);
    return;
  }
  const tourId = isRunnableTour(state.tourId) ? state.tourId : tourOrder[0];
  startOnboarding(tourId, state.stepIndex || 0);
}

export function stopOnboarding(markDismissed = true) {
  activeTourRun += 1;
  state.active = false;
  state.dismissed = !!markDismissed;
  persist();
  hideOverlay();
  updateTrigger();
}

function bindOverlay() {
  const overlay = deps.overlay;
  if (!overlay || overlay.dataset.bound === "1") return;
  overlay.dataset.bound = "1";
  deps.trigger?.addEventListener("click", () => {
    if (state.active) {
      stopOnboarding(true);
      return;
    }
    if (state.completed && !state.dismissed) {
      startOnboarding(tourOrder[0], 0);
      return;
    }
    if (state.tourId || state.stepIndex) {
      resumeOnboarding();
      return;
    }
    startOnboarding(tourOrder[0], 0);
  });
  deps.prev?.addEventListener("click", previousStep);
  deps.next?.addEventListener("click", nextStep);
  deps.skip?.addEventListener("click", () => stopOnboarding(true));
  window.addEventListener("resize", () => requestSync(false));
  window.addEventListener("scroll", handleViewportMove, { capture: true, passive: true });
  window.addEventListener("wheel", handleViewportMove, { capture: true, passive: true });
  document.addEventListener("keydown", event => {
    if (!state.active) return;
    if (event.key === "Escape") stopOnboarding(true);
    if (event.key === "ArrowRight" && !event.metaKey && !event.ctrlKey) nextStep();
    if (event.key === "ArrowLeft" && !event.metaKey && !event.ctrlKey) previousStep();
  });
}

function routeToCurrentStep() {
  const step = currentStep();
  if (!step) return;
  if (location.hash.slice(1) !== step.route) {
    location.hash = step.route;
    return;
  }
  requestSync(true);
}

function handleViewportMove() {
  if (!state.active) {
    if (isOverlayVisible()) hideOverlay();
    return;
  }
  markOverlayScrolling();
  requestSync(false);
}

function currentTour() {
  return isRunnableTour(state.tourId) ? tours[state.tourId] : null;
}

function currentStep() {
  const tour = currentTour();
  return tour?.steps?.[state.stepIndex] || null;
}

function emitOnboardingStep(step) {
  window.dispatchEvent(new CustomEvent("goflow:onboarding-step", {
    detail: {
      tourId: state.tourId,
      stepIndex: state.stepIndex,
      step
    }
  }));
}

function nextStep() {
  const tour = currentTour();
  if (!tour) {
    startOnboarding(tourOrder[0], 0);
    return;
  }
  if (state.stepIndex < tour.steps.length - 1) {
    state.stepIndex += 1;
    persist();
    routeToCurrentStep();
    return;
  }
  const currentIndex = tourOrder.indexOf(tour.id);
  if (currentIndex >= 0 && currentIndex < tourOrder.length - 1) {
    state.tourId = tourOrder[currentIndex + 1];
    state.stepIndex = 0;
    persist();
    routeToCurrentStep();
    return;
  }
  state.active = false;
  state.completed = true;
  state.dismissed = true;
  state.tourId = "";
  state.stepIndex = 0;
  persist();
  hideOverlay();
  updateTrigger();
}

function previousStep() {
  const tour = currentTour();
  if (!tour) return;
  if (state.stepIndex > 0) {
    state.stepIndex -= 1;
    persist();
    routeToCurrentStep();
    return;
  }
  const currentIndex = tourOrder.indexOf(tour.id);
  if (currentIndex > 0) {
    const previousTour = tours[tourOrder[currentIndex - 1]];
    state.tourId = previousTour.id;
    state.stepIndex = previousTour.steps.length - 1;
    persist();
    routeToCurrentStep();
  }
}

function syncActiveStep() {
  if (!state.active) {
    hideOverlay();
    return;
  }
  const step = currentStep();
  const overlay = deps.overlay;
  if (!step || !overlay) return;
  const stepKey = `${state.tourId}:${step.id}`;
  if (stepKey !== lastStepKey) {
    lastStepKey = stepKey;
    targetRetryCount = 0;
  }
  emitOnboardingStep(step);
  const target = document.querySelector(step.target);
  const content = deps.contentRoot || document.body;
  if (!target || !content.contains(target) || !isVisibleTarget(target)) {
    scheduleTargetRetry(stepKey);
    updateOverlayContent(step, null);
    overlay.classList.remove("hidden");
    overlay.setAttribute("aria-hidden", "false");
    overlay.classList.add("is-fallback");
    positionPopover(null);
    activeTarget?.classList.remove("tour-target-active");
    activeTarget = null;
    return;
  }
  clearTargetRetry();
  targetRetryCount = 0;
  overlay.classList.remove("hidden", "is-fallback");
  overlay.setAttribute("aria-hidden", "false");
  if (syncShouldAlign && !isTargetComfortablyVisible(target)) {
    target.scrollIntoView({ block: "center", inline: "nearest", behavior: "auto" });
    syncShouldAlign = false;
    requestSync(false);
    return;
  }
  syncShouldAlign = false;
  const rect = visibleTargetRect(target.getBoundingClientRect());
  const left = Math.max(16, rect.left - 14);
  const top = Math.max(16, rect.top - 14);
  const maxWidth = Math.max(120, window.innerWidth - left - 16);
  const maxHeight = Math.max(64, window.innerHeight - top - 16);
  overlay.style.setProperty("--tour-left", `${left}px`);
  overlay.style.setProperty("--tour-top", `${top}px`);
  overlay.style.setProperty("--tour-width", `${Math.min(maxWidth, rect.width + 28)}px`);
  overlay.style.setProperty("--tour-height", `${Math.min(maxHeight, rect.height + 28)}px`);
  updateOverlayContent(step, target);
  positionPopover(rect);
  if (activeTarget && activeTarget !== target) activeTarget.classList.remove("tour-target-active");
  activeTarget = target;
  activeTarget.classList.add("tour-target-active");
}

function isVisibleTarget(target) {
  const rect = target.getBoundingClientRect();
  const style = window.getComputedStyle(target);
  return rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none";
}

function isTargetComfortablyVisible(target) {
  const rect = target.getBoundingClientRect();
  const pad = 24;
  const topPad = stickyTopOffset() + pad;
  return rect.top >= topPad &&
    rect.left >= pad &&
    rect.bottom <= window.innerHeight - pad &&
    rect.right <= window.innerWidth - pad;
}

function visibleTargetRect(rect) {
  const topLimit = stickyTopOffset() + 8;
  const left = Math.max(0, rect.left);
  const top = Math.max(topLimit, rect.top);
  const right = Math.min(window.innerWidth, rect.right);
  const bottom = Math.min(window.innerHeight, rect.bottom);
  if (right - left >= 24 && bottom - top >= 24) {
    return { left, top, right, bottom, width: right - left, height: bottom - top };
  }
  return rect;
}

function stickyTopOffset() {
  const topbar = document.querySelector(".topbar");
  if (!topbar) return 0;
  const rect = topbar.getBoundingClientRect();
  return rect.top <= 1 && rect.bottom > 0 ? Math.min(rect.bottom, window.innerHeight * 0.4) : 0;
}

function positionPopover(targetRect) {
  const overlay = deps.overlay;
  const popover = deps.popover;
  if (!overlay || !popover) return;
  const gap = 18;
  const margin = 16;
  const width = Math.min(popover.offsetWidth || 372, window.innerWidth - margin * 2);
  const height = Math.min(popover.offsetHeight || 260, window.innerHeight - margin * 2);
  let left = Math.round((window.innerWidth - width) / 2);
  let top = Math.round((window.innerHeight - height) / 2);

  if (targetRect) {
    left = targetRect.right + gap;
    top = targetRect.top;
    if (left + width > window.innerWidth - margin) {
      left = targetRect.left - width - gap;
    }
    if (left < margin) {
      left = Math.min(Math.max(margin, targetRect.left), window.innerWidth - width - margin);
      top = targetRect.bottom + gap;
      if (top + height > window.innerHeight - margin) {
        top = targetRect.top - height - gap;
      }
    }
  }

  left = Math.min(Math.max(margin, left), Math.max(margin, window.innerWidth - width - margin));
  top = Math.min(Math.max(margin, top), Math.max(margin, window.innerHeight - height - margin));
  overlay.style.setProperty("--tour-popover-left", `${Math.round(left)}px`);
  overlay.style.setProperty("--tour-popover-top", `${Math.round(top)}px`);
}

function updateOverlayContent(step, target) {
  const tour = currentTour();
  const absoluteIndex = absoluteStepIndex();
  const absoluteTotal = absoluteStepTotal();
  deps.badge.textContent = deps.t(tour?.titleKey || "tour.group.shell");
  deps.counter.textContent = `${absoluteIndex} / ${absoluteTotal}`;
  deps.title.textContent = deps.t(step.titleKey);
  deps.body.textContent = deps.t(step.bodyKey);
  deps.meta.textContent = target ? deps.t("tour.meta.interactive") : deps.t("tour.meta.fallback");
  const isFirst = absoluteIndex === 1;
  const nextLabel = absoluteIndex === absoluteTotal ? deps.t("tour.finish") : deps.t("tour.next");
  deps.prev.disabled = isFirst;
  deps.prev.setAttribute("aria-disabled", isFirst ? "true" : "false");
  deps.prev.setAttribute("aria-label", isFirst ? deps.t("tour.backUnavailable") : deps.t("tour.back"));
  deps.prev.title = isFirst ? deps.t("tour.backUnavailable") : deps.t("tour.back");
  deps.next.textContent = nextLabel;
  deps.next.setAttribute("aria-label", nextLabel);
  deps.next.title = nextLabel;
  deps.skip.setAttribute("aria-label", deps.t("tour.skip"));
  deps.skip.title = deps.t("tour.skip");
}

function absoluteStepIndex() {
  const tour = currentTour();
  if (!tour) return 1;
  let offset = 0;
  for (const id of tourOrder) {
    if (id === tour.id) break;
    offset += tours[id].steps.length;
  }
  return offset + state.stepIndex + 1;
}

function absoluteStepTotal() {
  return tourOrder.reduce((sum, id) => sum + tours[id].steps.length, 0);
}

function requestSync(align = false) {
  if (!state.active) {
    if (isOverlayVisible()) hideOverlay();
    return;
  }
  syncShouldAlign = syncShouldAlign || !!align;
  const token = ++syncToken;
  const tourRun = activeTourRun;
  window.cancelAnimationFrame(syncFrame);
  syncFrame = window.requestAnimationFrame(() => {
    if (token === syncToken && tourRun === activeTourRun) syncActiveStep();
  });
}

function hideOverlay() {
  const overlay = deps?.overlay;
  if (!overlay) return;
  const alreadyHidden = overlay.classList.contains("hidden") && overlay.getAttribute("aria-hidden") === "true";
  if (alreadyHidden && !activeTarget && !syncFrame && !targetRetryTimer) return;
  syncToken++;
  window.cancelAnimationFrame(syncFrame);
  syncFrame = 0;
  clearTargetRetry();
  window.clearTimeout(scrollSyncTimer);
  scrollSyncTimer = 0;
  overlay.classList.add("hidden");
  overlay.setAttribute("aria-hidden", "true");
  overlay.classList.remove("is-fallback", "is-scrolling");
  [
    "--tour-left",
    "--tour-top",
    "--tour-width",
    "--tour-height",
    "--tour-popover-left",
    "--tour-popover-top"
  ].forEach(name => overlay.style.removeProperty(name));
  activeTarget?.classList.remove("tour-target-active");
  activeTarget = null;
  syncShouldAlign = false;
}

function scheduleTargetRetry(stepKey) {
  if (targetRetryCount >= targetRetryLimit) return;
  targetRetryCount += 1;
  clearTargetRetry();
  const tourRun = activeTourRun;
  targetRetryTimer = window.setTimeout(() => {
    targetRetryTimer = 0;
    if (!state.active || tourRun !== activeTourRun || currentStepKey() !== stepKey) return;
    requestSync(true);
  }, targetRetryDelayMs);
}

function clearTargetRetry() {
  if (!targetRetryTimer) return;
  window.clearTimeout(targetRetryTimer);
  targetRetryTimer = 0;
}

function currentStepKey() {
  const step = currentStep();
  return step ? `${state.tourId}:${step.id}` : "";
}

function isOverlayVisible() {
  const overlay = deps?.overlay;
  return !!overlay && (!overlay.classList.contains("hidden") || overlay.getAttribute("aria-hidden") === "false");
}

function markOverlayScrolling() {
  const overlay = deps?.overlay;
  if (!overlay) return;
  overlay.classList.add("is-scrolling");
  window.clearTimeout(scrollSyncTimer);
  scrollSyncTimer = window.setTimeout(() => {
    scrollSyncTimer = 0;
    overlay.classList.remove("is-scrolling");
    if (state.active) requestSync(false);
  }, 140);
}

function updateTrigger() {
  if (!deps?.trigger) return;
  if (state.active) {
    deps.trigger.textContent = deps.t("tour.stop");
    deps.trigger.setAttribute("aria-pressed", "true");
    deps.trigger.title = deps.t("tour.stop");
    return;
  }
  deps.trigger.setAttribute("aria-pressed", "false");
  if (state.completed) {
    deps.trigger.textContent = deps.t("tour.restart");
    deps.trigger.title = deps.t("tour.restart");
    return;
  }
  if (state.tourId || state.stepIndex) {
    deps.trigger.textContent = deps.t("tour.resume");
    deps.trigger.title = deps.t("tour.resume");
    return;
  }
  deps.trigger.textContent = deps.t("tour.start");
  deps.trigger.title = deps.t("tour.start");
}

function loadState() {
  try {
    const parsed = JSON.parse(localStorage.getItem(onboardingStorageKey) || "{}");
    if (parsed.completed && parsed.dismissed) {
      return { active: false, dismissed: true, completed: true, tourId: "", stepIndex: 0 };
    }
    const tourId = isRunnableTour(parsed.tourId) ? parsed.tourId : "";
    const tour = tourId ? tours[tourId] : null;
    return {
      active: false,
      dismissed: !!parsed.dismissed,
      completed: !!parsed.completed,
      tourId,
      stepIndex: tour ? clampStepIndex(tour, parsed.stepIndex) : 0
    };
  } catch {
    return { active: false, dismissed: false, completed: false, tourId: "", stepIndex: 0 };
  }
}

function isRunnableTour(tourId) {
  return !!tourId && tourOrder.includes(tourId) && !!tours[tourId];
}

function clampStepIndex(tour, stepIndex) {
  const parsed = Number(stepIndex);
  if (!tour?.steps?.length || !Number.isFinite(parsed)) return 0;
  return Math.min(Math.max(0, Math.trunc(parsed)), tour.steps.length - 1);
}

function persist() {
  localStorage.setItem(onboardingStorageKey, JSON.stringify({
    tourId: state.tourId,
    stepIndex: state.stepIndex,
    completed: state.completed,
    dismissed: state.dismissed
  }));
}
