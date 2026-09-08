---
name: Explorarte · Organización viva
description: Local mission demo with a connected pixel-art office floor.
colors:
  background: "#12251d"
  surface: "#192d23"
  line: "#48573b"
  text: "#f0ecd4"
  muted: "#bec5a9"
  accent: "#c0d49a"
  selected: "#405737"
  selected-border: "#81965d"
typography:
  body:
    fontFamily: "Segoe UI, system-ui, sans-serif"
    lineHeight: 1.6
  headline:
    fontSize: "28px"
  title:
    fontSize: "24px"
    lineHeight: 1.2
    letterSpacing: "-0.02em"
  section:
    fontSize: "16px"
    fontWeight: 500
  metadata:
    fontSize: "14px"
rounded:
  control: "5px"
  scene-label: "3px"
spacing:
  compact: "8px"
  small: "12px"
  medium: "16px"
  large: "20px"
  section: "24px"
components:
  button-default:
    backgroundColor: "transparent"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "8px 15px"
  button-primary:
    backgroundColor: "{colors.selected}"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "8px 15px"
  button-quiet:
    backgroundColor: "transparent"
    textColor: "{colors.muted}"
    rounded: "{rounded.control}"
    padding: "8px 15px"
  detail-panel:
    backgroundColor: "transparent"
    textColor: "{colors.text}"
    rounded: "{rounded.control}"
    padding: "18px 14px"
---

# Design System: Explorarte · Organización viva

## Overview

**Creative North Star: "Organización viva"**

Organización viva is a connected pixel-art workplace framed by quiet forest-green Operate controls. Warm wood, shelves, plants, and a central corridor make the departments recognizable; ivory text and sage selection keep the surrounding interface readable and restrained. The approved office direction supplies this visual language; ordinary system typography is intentional in the application chrome.

This reference applies only to `mission-demo.html` and `src/mission-demo/`. It replaces the former six-independent-canvas reference. The actual application in `index.html` and `src/app.js` is outside this design contract. The frontend uses native JavaScript without added packages. OfficeProjection is a presentation-neutral snapshot contract with a local simulation source and a read-only VPS adapter; this build contains no Agent Office runtime and does not execute kernel work from the browser.

**Key Characteristics:**

- One continuous furnished floor with six fixed rooms and a central corridor.
- Quiet HTML controls and contextual details around expressive raster scenery.
- Sage selection, ivory text, and persistent simulated-data disclosure.

## Colors

The interface uses forest greens, warm ivory, and a restrained sage accent around wood-rich pixel artwork. Frontmatter values record the implemented CSS vocabulary; `surface` is declared but does not currently provide a separate panel fill.

### Primary

Sage (`accent`) marks keyboard focus, status dots, completed step markers, and selected name borders. Moss (`selected`) fills selected navigation, tabs, primary controls, active steps, and selected character names; `selected-border` defines the accompanying control edge.

### Neutral

Forest (`background`) is the page foundation. Ivory (`text`) carries primary content; soft olive (`muted`) carries descriptions and metadata. Olive-brown (`line`) defines dividers and quiet control boundaries. Room labels, speech bubbles, and raster art have local scene colors, not additional global accents.

**The Selection Rule.** Selection belongs to room boundaries, control states, and character nameplates. Character hit areas remain transparent in both selected and hover states.

## Typography

The system stack in the frontmatter is shared by controls and content. It intentionally reads as normal Operate chrome; there is no pixel font or external font dependency. Paragraph line height applies to paragraphs, not a blanket body rule. Controls, small text, selects, and outputs use tabular numerals.

The wordmark is compact (29px, weight 650, tracking -0.03em), reducing to 25px at the mobile breakpoint. Page headlines use the headline role; inspector names use the title role. Section headings are restrained, while task copy and metadata use 14px. Event metadata and step status use 11px, disclosure text 12px. Desktop room labels scale with `clamp(12px, 1.2vw, 19px)` and character names use 11px.

At widths up to 900px, room labels become 10px and character names 9px. Those map labels are a known minor visual limitation at fit-to-floor size; zoom and the HTML department directory provide alternate access. Do not describe the fit view as uniformly legible at every narrow width.

## Layout

The page fills the window. A compact top navigation bar (minimum 68px) sits above the mission selector, phase label, and playback controls (minimum 62px). The main desktop workspace is a flexible floor column plus a 348px inspector. The inspector expands to 380px from 1600px and narrows to 310px at 1100px. It uses a left divider rather than a floating card.

One raster floor has an aspect ratio of 1495:1052. Six fixed rooms occupy two rows of three around the connecting corridor: Dirección, Investigación, and Ingeniería above; Negocio, Skills, and Servicios below. Percentage-positioned HTML room buttons and character canvases overlay that floor. Room positions and seats remain stable across missions and phases. Empty rooms retain their furniture and show “Sin asignaciones”.

The desktop floor viewport scrolls within `calc(100dvh - 205px)` and retains a 300px minimum height; the floor sizer has a 720px minimum width. Zoom ranges from 100% to 200% in 25% increments. “Piso completo” resets zoom; “Centrar” horizontally centers the viewport and returns it to the top. The toolbar, expandable department directory, and selection hint follow the map. The directory uses three columns on desktop.

At 900px and below, navigation and mission controls wrap, the floor fits the available width with no minimum scene width, and the inspector stacks beneath it. The floor viewport has a 550px maximum height. The directory becomes two columns; the Missions view changes from three cards to one column. Selection focuses and scrolls to the inspector heading; the visible return control restores focus to the character or room. Speech bubbles are hidden on these widths.

## Elevation & Depth

HTML surfaces are flat: borders, selected fills, and spacing provide hierarchy without box shadows. The raster floor carries the scene’s material depth. Room selection uses a thin inset outline; character selection uses its nameplate only, with no bounding rectangle. The small research speech bubble supplies a local foreground layer above the room labels and sprites.

## Shapes

Controls and inspector panels share gently rounded corners; scene labels use a smaller radius. Inspector tabs join into one segmented group with only the outer corners rounded. Step indices and the status indicator are circular. The floor is one rectangular, proportional illustration. Preserve pixelated image rendering and disabled canvas smoothing; do not stretch the floor into a square or smooth the sprites.

## Components

### Navigation, mission selector, and cards

Organization and Missions use pressed-state buttons. Inactive navigation is muted with transparent borders; active navigation uses the shared selected fill. A native labeled select changes the mission. The Missions view presents three spacious button cards with mission identifier, phase, assigned count, and an “Abrir oficina” action. Selecting a mission pauses playback and selects Investigación.

### Buttons and focus

Standard buttons and selects have a 44px minimum height and the frontmatter control padding. Hover uses the selected fill; disabled controls reduce opacity and lose the hover fill. Primary buttons use the selected fill and selected border. Quiet actions use muted text with a transparent border. Keyboard focus uses a 3px sage outline with 3px offset. Map overlays are specialized compact controls; mobile sprites have a 36px minimum width, and zoom controls reduce to 40px minimum width.

### Connected floor and characters

The floor image supplies furnished scenery; room labels and character buttons supply interaction. The selected room receives a 2px outline inset by 3px. Each character is a decorative canvas inside an accessible, named button with pressed state. Character names remain HTML. The final sprite rule uses normal blending, transparent selected backgrounds, and no selected pseudo-element frame. Keyboard focus remains visible.

Only the generated `connected-floor.png` and `team-sheet.png` are loaded as visual assets. The sprite renderer removes the pale sheet background in memory; original images remain intact. See `src/mission-demo/assets/OFFICE-A-PROVENANCE.md` for generation history. Legacy Star Office / LimeZu assets are not consumed by this demo; their earlier attribution does not describe the new floor. No paid Agent Office tileset is included.

### Inspector and disclosure

The inspector shows either department membership or the selected person’s task and mission. Tarea, Evidencia, and Actividad form an accessible tablist with arrow-key, Home, and End navigation. Bordered detail and recent-event panels contain phase steps, explicitly fictitious evidence, and simulated events. Evidence appears from the review phase onward and opens inline. The demo badge, event tags, note, and footer reinforce local simulation.

### Playback and motion

Play advances the mock phase every five seconds; advance and reset are manual controls. Completion disables play and advance. Each mission retains its phase and event history in memory. Rendering restores keyboard focus, falling back to reset when the prior control becomes disabled. During playback sprites bob vertically by 2px over a 1.6-second ease-in-out cycle; reduced-motion preference suppresses this decorative movement. There is no walking between rooms or skeletal animation.

## Do's and Don'ts

- Do preserve the connected six-room floor and fixed department positions.
- Do keep CEO and Investigación visible and show other profiles only when assigned to the mission.
- Do preserve keyboard focus, zoom, the HTML directory, and return-to-office navigation.
- Do identify tasks, evidence, events, and progression as local simulation.
- Do preserve asset provenance and embedded generation prompts when replacing artwork.
- Don't reintroduce six separate square office canvases.
- Don't fill a selected character’s entire hit area or restore its rectangular selection frame.
- Don't claim live agent execution or Agent Office runtime integration. The `?source=vps` route may label snapshot/report data as observed and must keep the read-only boundary visible.
- Don't apply this local demo’s scene palette or composition to unrelated application screens.
- Don't describe the result as measured pixel-perfect or fully cleared by automated comp gates.

The local finish review passed its scope with minor visual findings, including tiny mobile map labels. Strict automated comparison gates remain incomplete; review screenshots under `.impeccable/review/` are evidence of inspected views, not proof of measured pixel equivalence to the approved composition.
