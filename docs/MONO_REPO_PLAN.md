# IncentivizeThis: Flutter → HTML Template Migration Plan

## Executive Summary

This document outlines the high-level migration strategy for transitioning the IncentivizeThis Flutter/Dart frontend to a server-rendered HTML template-based approach integrated with the existing backend API.

**Current State:** Separate Flutter web app (SPA) that communicates with a backend API via HTTP
**Target State:** Server-rendered HTML templates with progressive enhancement, living within the backend codebase

---

## 1. Architecture Overview

### Current Architecture
```
Flutter Web App (SPA)          Backend API
├─ Client-side routing         ├─ REST endpoints
├─ Client-side state          ├─ Business logic
├─ API HTTP calls             ├─ Database access
└─ Static asset serving       └─ Authentication
```

### Target Architecture
```
Backend Monorepo
├─ Server-side routing (all routes)
├─ HTML template rendering
├─ Session-based state management
├─ Internal service calls (no HTTP overhead)
├─ Static asset serving
└─ Authentication via cookies/sessions
```

---

## 2. Routes Migration

### Current Routes (go_router)
| Route | Screen | Purpose |
|-------|--------|---------|
| `/` | MarketingScreen | Landing page with parallax, video, CTAs |
| `/bounties` | HomeScreen | Browse/search/filter bounties |
| `/bounties/:bountyId` | BountyDetailScreen | View bounty details + claim activity |
| `/about` | AboutScreen | Static information page |

### Migration Strategy

**Approach:** Map each Flutter route to a server-side route handler

**Implementation:**
- Direct 1:1 route mapping (same URL structure)
- Server-side route handlers render complete HTML pages
- Use URL query parameters for filters/search (e.g., `/bounties?platform=reddit&sort=reward`)
- Maintain deep linking capability for all routes
- Use proper HTTP status codes (200, 404, 500)

**Technology Options:**
- Template engine: Jinja2 (Python), Handlebars/EJS (Node.js), Go templates, etc.
- Framework: Express, Flask, Django, Go, FastAPI, etc. (depends on existing backend)

---

## 3. Page Templates

### 3.1 Marketing Page (`/`)

**Current Features:**
- Parallax scrolling background with carrot image
- Animated hero section with typed effect
- Clueso video player (WebView)
- 5 content sections with scroll-triggered animations
- Platform showcase with rotating examples
- CTA buttons (Create Bounty, View Bounties, Contact Us)
- Responsive desktop/mobile layouts

**Migration Requirements:**
- **Template:** `marketing.html` or equivalent
- **Static Assets:**
  - `marketing-carrot.jpg` (hero background)
  - `marketing-browse.png`, `marketing-content.png`, `marketing-paid.png`, `marketing-yuck-full.png`
- **JavaScript:**
  - Parallax scroll effects (Intersection Observer API)
  - Scroll-triggered animations
  - Platform showcase rotation
  - Video embed handling
- **CSS:**
  - Responsive breakpoints (450px, 600px, 700px)
  - Animation keyframes
  - Grid/flexbox layouts

**Implementation Notes:**
- Use native HTML5 `<video>` or iframe for Clueso player
- Implement scroll effects with vanilla JS or lightweight library (AOS, ScrollReveal)
- Maintain exact visual design with CSS transitions

---

### 3.2 Home/Bounties Page (`/bounties`)

**Current Features:**
- Two-section layout: Active Bounties + Recently Paid
- Real-time polling (5-second intervals)
- Search functionality (bottom sheet)
- Filter/sort system (platform, reward range, sort order)
- Pull-to-refresh support
- Empty states for no results
- Centered constrained layout (600px max width)

**Migration Requirements:**
- **Template:** `bounties.html`
- **Data Flow:**
  - Server fetches bounties on page load
  - Initial render with server-side data
  - Client-side polling for updates (optional, see alternatives below)
  - Search/filter via form submissions or AJAX
- **Components:**
  - Bounty card grid/list
  - Search input (header or inline)
  - Filter panel (collapsible sidebar or modal)
  - Empty state messages
- **Real-time Updates (Choose One):**
  - **Option A:** Full page refresh on user action (simplest)
  - **Option B:** AJAX polling every 5 seconds (update specific DOM sections)
  - **Option C:** Server-Sent Events (SSE) for push updates
  - **Option D:** WebSocket connection for bi-directional updates

**Recommended Approach:** Start with Option A (full page refresh on filter/search), optionally add Option B for live updates

**Filter/Search Implementation:**
- Use HTML forms with GET method (preserves URL state)
- Query parameters: `?q=search&platform=reddit,youtube&reward=medium&sort=deadline&order=asc`
- Server parses params and filters results
- Pre-populate form inputs from query params (maintain state)

---

### 3.3 Bounty Detail Page (`/bounties/:id`)

**Current Features:**
- Detailed bounty view with 10-second polling
- Metadata info chips (reward, platform, deadline, remaining, tier)
- Markdown description rendering
- Claim button (state-aware: active, disabled, loading)
- Claim activity timeline (paid bounties)
- Fund bounty button for awaiting funding status
- Floating app bar

**Migration Requirements:**
- **Template:** `bounty_detail.html`
- **Data Flow:**
  - Server fetches bounty + paid bounties on page load
  - Render complete page with all data
  - Optional polling for status updates
- **Components:**
  - Info badges/chips
  - Markdown-to-HTML conversion (server-side)
  - Claim button (opens claim modal or redirects to claim page)
  - Fund button (opens funding modal)
  - Activity timeline
- **Markdown Rendering:**
  - Use server-side Markdown library (markdown-it, marked, Python-Markdown, etc.)
  - Sanitize HTML output to prevent XSS
  - Apply consistent styling

**Implementation Notes:**
- Claim/Fund buttons trigger modals or navigate to separate pages
- Update polling can refresh page sections via AJAX
- Maintain back navigation to previous search/filter state

---

### 3.4 About Page (`/about`)

**Current Features:**
- Static informational content
- Step-by-step guides for businesses and creators
- Discord link
- Centered constrained layout

**Migration Requirements:**
- **Template:** `about.html`
- Fully static content, no dynamic data
- Simple server-side render

---

## 4. Dialogs/Modals Migration

### Current Dialogs
1. **ClaimDialog** - Submit bounty claim
2. **CreateBountyDialog** - Create new bounty
3. **AuthPromptDialog** - Enter JWT token
4. **ContactUsDialog** - Contact form
5. **FundingQrDialog** - Display payment QR code
6. **SearchBountiesSheet** - Search input
7. **FilterSheet** - Filter/sort options

### Migration Strategy

**Approach Options:**

#### Option A: Inline Modals with CSS/JS
- Render modal HTML in page template (hidden by default)
- Toggle visibility with JavaScript
- Form submissions via AJAX or full page post
- **Pros:** No navigation, smooth UX
- **Cons:** Requires more JavaScript

#### Option B: Separate Pages
- Each dialog becomes a separate route (e.g., `/bounties/create`, `/bounties/claim/:id`)
- Use query parameters to return to previous page
- **Pros:** Simple, works without JS, bookmarkable
- **Cons:** Navigation overhead, page reloads

#### Option C: Hybrid Approach (Recommended)
- Use inline modals for simple dialogs (auth, contact, search)
- Use separate pages for complex forms (create bounty, claim bounty)
- Use HTMX or similar for progressive enhancement

---

### 4.1 Claim Dialog → Claim Page/Modal

**Current Features:**
- Content ID/URL input with platform-specific parsing
- Wallet address input with persistence
- Real-time URL parsing feedback
- Help section with wallet provider links
- Form validation
- Loading state during submission

**Migration Requirements:**
- **Template:** `claim_modal.html` or `/bounties/:id/claim` page
- **Form Fields:**
  - Content ID/URL (text input)
  - Wallet address (text input)
  - Platform dropdown (optional, for non-URL content IDs)
- **Validation:**
  - Server-side validation on submit
  - Client-side validation with HTML5 attributes + JS
  - Error message display
- **Content ID Parsing:**
  - Implement URL parsing on server (regex patterns)
  - AJAX endpoint to validate/parse content ID in real-time
  - Or parse on submit and show errors
- **Wallet Persistence:**
  - Store in session/cookie
  - Pre-fill on subsequent claims
- **Submission Flow:**
  - POST to `/api/bounties/:id/claim` or similar
  - On success: redirect to bounty detail with success message
  - On error: re-render form with error messages

---

### 4.2 Create Bounty Dialog → Create Bounty Page

**Current Features:**
- Multi-step form: Requirements → Funding details
- Requirements editor with "Harden" AI feature (animated glow)
- Numeric inputs: bounty per post, number of bounties
- Duration selection (1d-180d)
- Cost breakdown calculator (bounty + platform fee)
- Transitions to QR funding view on success

**Migration Requirements:**
- **Template:** `create_bounty.html` (single page or multi-step wizard)
- **Form Sections:**
  1. Requirements text area
     - "Harden" button → AJAX call to `/api/bounties/harden`
     - Show loading spinner during AI processing
     - Replace text area content with hardened requirements
  2. Funding details
     - Platform dropdown
     - Content kind dropdown
     - Bounty per post (number input)
     - Number of bounties (number input)
     - Duration (dropdown or number input)
     - Real-time cost calculator (JavaScript)
- **Multi-step Implementation:**
  - Option A: Single page with show/hide sections (JavaScript)
  - Option B: Separate routes with session state (`/bounties/create/step1`, `/bounties/create/step2`)
  - Option C: Use form wizard library (StepForm, etc.)
- **Submission Flow:**
  - POST to `/api/bounties` (requires authentication)
  - On success: redirect to funding QR page or modal
  - On error: re-render with validation errors

---

### 4.3 Funding QR Dialog → Funding QR Page

**Current Features:**
- QR code generation for Solana USDC payments
- Countdown timer for payment expiration
- Solana deep link support (`solana://` protocol)
- Embedded QR center image
- "Open in Wallet" button

**Migration Requirements:**
- **Template:** `funding_qr.html` or inline modal
- **QR Code Generation:**
  - Generate QR code server-side (qrcode library in Python, qrcode.js in Node, etc.)
  - Or generate client-side with JavaScript library (qrcode.js)
  - Embed Solana payment URI: `solana:{address}?amount={amount}&spl-token={mint}`
- **Components:**
  - QR code image or SVG
  - Countdown timer (JavaScript)
  - "Open in Wallet" button (deeplink)
  - Payment address display (copy-to-clipboard)
- **Implementation:**
  - Pass payment details (address, amount, expiration) from server
  - JavaScript countdown updates display
  - Auto-refresh or poll for payment confirmation
  - Redirect to bounty detail on successful payment

---

### 4.4 Auth Prompt Dialog → Auth Modal

**Current Features:**
- JWT token input
- Token format validation (3-part structure)
- Gumroad checkout link

**Migration Requirements:**
- **Template:** `auth_modal.html` (inline overlay)
- **Trigger:** Show when accessing protected routes or actions
- **Form:**
  - JWT token input (password field)
  - "Get Token" link to Gumroad
  - Submit button
- **Validation:**
  - Client-side: check 3-part JWT format with regex
  - Server-side: verify JWT signature and expiration
- **Flow:**
  - User submits token
  - Server validates and stores in session/cookie
  - Redirect to original intended action
  - Or show error and allow retry

---

### 4.5 Contact Us Dialog → Contact Modal/Page

**Current Features:**
- Simple form: name (optional), email, message
- Email validation
- Success notification

**Migration Requirements:**
- **Template:** `contact_modal.html` or `/contact` page
- **Form Fields:**
  - Name (text input, optional)
  - Email (email input, required)
  - Message (textarea, required)
- **Validation:**
  - HTML5 validation attributes
  - Server-side validation
- **Submission:**
  - POST to `/api/contact-us`
  - On success: show success message or redirect
  - On error: show error message

---

### 4.6 Search Sheet → Search Input

**Current Features:**
- Bottom sheet with search input
- Auto-focus on appear
- Submit on enter or button press

**Migration Requirements:**
- **Implementation:** Header search bar or inline search form
- **Form:**
  - Text input with placeholder
  - Search button or submit on enter
  - GET request to `/bounties?q={query}`
- **UX:**
  - Auto-focus can use `autofocus` HTML attribute
  - Preserve search term in input after submit
  - Clear button to reset search

---

### 4.7 Filter Sheet → Filter Panel

**Current Features:**
- Platform selection (multi-select chips)
- Reward range selection (single select chips)
- Sort options (single select chips)
- Reset and apply buttons

**Migration Requirements:**
- **Implementation Options:**
  - Sidebar panel (collapsible)
  - Top bar with dropdowns
  - Modal overlay
  - Inline form above results
- **Form Elements:**
  - Platform: checkboxes or multi-select dropdown
  - Reward range: radio buttons or dropdown
  - Sort order: dropdown or radio buttons
- **Submission:**
  - GET request to `/bounties?platform=reddit,youtube&reward=medium&sort=deadline`
  - Apply button submits form
  - Reset button clears all selections and reloads

---

## 5. Static Assets Migration

### Current Assets
- **Images:**
  - `favicon.png`
  - `marketing-carrot.jpg` (hero background, ~500KB+)
  - `marketing-browse.png`, `marketing-content.png`, `marketing-paid.png`
  - `marketing-yuck-full.png`
  - `qr-center.png` (QR code logo)
- **Icons:** Material Icons (replaced with icon font or SVG sprites)
- **Platform Icons:** Mapped from platform kind (Reddit, YouTube, etc.)

### Migration Strategy

**Directory Structure:**
```
/backend
  /static
    /images
      - favicon.png
      - marketing-carrot.jpg
      - marketing-browse.png
      - marketing-content.png
      - marketing-paid.png
      - marketing-yuck-full.png
      - qr-center.png
    /css
      - main.css
      - components.css
    /js
      - main.js
      - components.js
    /fonts (optional)
      - icon-font.woff2
```

**Serving:**
- Configure backend to serve `/static/*` as static files
- Use CDN for production (CloudFront, Cloudflare, etc.)
- Implement caching headers for immutable assets
- Consider image optimization (WebP, compression)

**Platform Icons:**
- Option A: Use icon font (Font Awesome, Material Icons CDN)
- Option B: SVG sprites embedded in HTML
- Option C: Individual SVG files served as static assets
- **Mapping:** Server-side mapping from platform kind to icon class/name

---

## 6. State Management Migration

### Current State (Client-side)
- **Provider pattern:** ApiService, StorageService, AppConfigService
- **StatefulWidget:** Local component state
- **Polling timers:** Real-time updates
- **SharedPreferences:** Wallet address, auth token, recent bounties

### Target State (Server-side)

**Session-based State:**
- Use cookies/sessions for user-specific state
- Store authentication token in HTTP-only secure cookie
- Store wallet address in session
- Store filter/search preferences in session (optional)

**No Persistent Local State:**
- Remove SharedPreferences equivalent
- All persistent data on server (database or cache)

**Real-time Updates:**
- Option A: No polling, users refresh manually
- Option B: AJAX polling with JavaScript (update DOM fragments)
- Option C: Server-Sent Events for push updates
- Option D: WebSockets for real-time bidirectional updates

**Configuration:**
- Environment variables or config files
- Load once on server startup
- Pass to templates as needed (Gumroad URL, API URLs, etc.)

---

## 7. Forms & Validation

### Current Validation (Client-side)
- Flutter Form + FormField widgets
- Custom validators for email, JWT, numbers, required fields
- AutovalidateMode for real-time feedback

### Target Validation

**Server-side Validation (Required):**
- Validate all inputs on server before processing
- Return errors as part of response
- Re-render form with error messages

**Client-side Validation (Optional, Progressive Enhancement):**
- HTML5 validation attributes (`required`, `type="email"`, `pattern`, `min`, `max`)
- JavaScript validation for better UX (instant feedback)
- Disable submit button while invalid

**Implementation Pattern:**
```html
<form method="POST" action="/bounties/create">
  <input type="text" name="title" required minlength="5" maxlength="100">
  <span class="error">{{ errors.title }}</span>

  <input type="number" name="bounty_per_post" required min="1" step="0.01">
  <span class="error">{{ errors.bounty_per_post }}</span>

  <button type="submit">Create Bounty</button>
</form>
```

**Error Display:**
- Inline field errors (below input)
- Form-level error summary (top of form)
- Use flash messages for success/info notifications

---

## 8. Authentication & Authorization

### Current System
- JWT token from Gumroad purchase
- Stored in SharedPreferences (local storage)
- Auto-included in API requests (Authorization header)
- AuthPromptDialog on 401 errors

### Target System

**Cookie-based Sessions (Recommended):**
- User submits JWT token via auth form
- Server validates JWT
- Server creates session and sets HTTP-only secure cookie
- Cookie auto-included in all subsequent requests
- Server checks session on protected routes
- Session expires after timeout or logout

**Implementation:**
```
User Flow:
1. User clicks "Create Bounty" (protected action)
2. Server checks for valid session
3. If no session: redirect to /auth?redirect=/bounties/create
4. User enters JWT token from Gumroad
5. Server validates JWT, creates session, sets cookie
6. Redirect to original URL (/bounties/create)
```

**Security:**
- CSRF protection (CSRF tokens in forms)
- HTTP-only cookies (prevent XSS)
- Secure flag (HTTPS only)
- SameSite attribute
- Session expiration and renewal

**Protected Routes:**
- Create bounty
- Submit claim
- Harden requirements
- Fund bounty

---

## 9. API Integration

### Current Integration
- Separate Flutter app makes HTTP requests to backend API
- Network overhead for every request
- CORS configuration required
- Client-side error handling

### Target Integration

**Internal Service Calls:**
- Backend templates call internal service layer directly
- No HTTP overhead
- No CORS issues
- Shared authentication context

**Adapter Pattern:**
```
Old: Flutter → HTTP → Backend API → Business Logic
New: Backend Route Handler → Business Logic (direct call)
```

**Implementation:**
- Route handlers call service layer methods
- Pass data to templates for rendering
- Handle errors and render error pages/messages
- No need for separate API endpoints (unless exposing public API)

**API Endpoints (if exposing to external clients):**
- AJAX endpoints for dynamic updates
- Keep existing REST API for potential future mobile apps
- Share business logic between template routes and API routes

---

## 10. Special Features Migration

### 10.1 Real-time Updates (Polling)

**Current:** JavaScript timers poll API every 5-10 seconds

**Options:**
- **A. Manual Refresh:** Users refresh page to see updates (simplest)
- **B. AJAX Polling:** JavaScript polls endpoint, updates DOM fragments
- **C. Server-Sent Events (SSE):** Server pushes updates to clients
- **D. WebSockets:** Full bidirectional real-time communication

**Recommendation:** Start with manual refresh (A), add AJAX polling (B) if needed

**AJAX Polling Implementation:**
```javascript
// JavaScript
setInterval(async () => {
  const response = await fetch('/api/bounties?format=json');
  const bounties = await response.json();
  updateBountyList(bounties);
}, 5000);
```

---

### 10.2 Animations & Interactions

**Current Animations:**
- Parallax scrolling on marketing page
- Scroll-triggered fade/slide animations
- Platform showcase rotation
- Glow effect on "Harden" button
- Arrow bounce
- Loading spinners

**Migration:**
- Use CSS animations and transitions
- Intersection Observer API for scroll-triggered animations
- JavaScript for complex interactions (platform rotation)
- Libraries: AOS (Animate On Scroll), ScrollReveal, GSAP (if needed)

**Example:**
```css
.fade-in {
  opacity: 0;
  transition: opacity 0.5s ease-in;
}

.fade-in.visible {
  opacity: 1;
}
```

```javascript
const observer = new IntersectionObserver((entries) => {
  entries.forEach(entry => {
    if (entry.isIntersecting) {
      entry.target.classList.add('visible');
    }
  });
});

document.querySelectorAll('.fade-in').forEach(el => observer.observe(el));
```

---

### 10.3 Content ID Parsing

**Current:** Client-side regex parsing of platform-specific URLs

**Migration:** Move to server-side parsing

**Implementation:**
- AJAX endpoint: `/api/parse-content-id` (POST with URL)
- Returns parsed platform and content ID
- Use for real-time feedback during claim submission
- Or parse on final form submission and show errors

---

### 10.4 QR Code Generation

**Current:** Client-side QR generation with qr_flutter

**Migration Options:**
- **A. Server-side:** Generate QR as image, serve as `<img src="/qr/{{ payment_id }}.png">`
- **B. Client-side:** Use JavaScript library (qrcode.js, kjua)

**Recommendation:** Server-side for consistency and security

**Libraries:**
- Python: `qrcode`, `segno`
- Node.js: `qrcode`, `qr-image`
- Go: `github.com/skip2/go-qrcode`

---

### 10.5 Markdown Rendering

**Current:** Client-side Markdown → HTML conversion

**Migration:** Server-side Markdown → HTML conversion

**Implementation:**
- Convert Markdown to HTML before rendering template
- Sanitize HTML to prevent XSS (use DOMPurify, bleach, etc.)
- Apply consistent CSS styling

**Libraries:**
- Python: `markdown`, `mistune`
- Node.js: `marked`, `markdown-it`
- Go: `github.com/gomarkdown/markdown`

---

### 10.6 Notifications/Toasts

**Current:** Flushbar library for toast notifications

**Migration Options:**
- **A. Flash messages:** Server-side flash messages (session-based)
- **B. JavaScript toasts:** Client-side toast library (Toastify, Notyf)
- **C. HTML5 Notifications API:** Browser notifications (requires permission)

**Recommendation:** Combination of flash messages (A) and JavaScript toasts (B)

**Flash Messages:**
- Set in session after form submission
- Display on next page load
- Auto-dismiss with JavaScript

**JavaScript Toasts:**
- For AJAX operations
- For real-time events
- Show success/error without page reload

---

## 11. Technology Stack Recommendations

### Backend Framework Options

**Python:**
- **Django:** Full-featured, built-in templates, admin, ORM
- **Flask:** Lightweight, flexible, Jinja2 templates
- **FastAPI:** Modern, async, automatic API docs

**Node.js:**
- **Express:** Minimal, flexible, huge ecosystem
- **Next.js:** Full-stack React framework (if you want some client-side React)
- **Fastify:** Fast, low overhead

**Go:**
- **Gin:** Fast HTTP framework
- **Echo:** Minimalist, high performance
- **html/template:** Built-in templating

**Considerations:**
- **Match existing backend:** Use same language/framework as current API
- **Team expertise:** Choose what team knows best
- **Performance:** All options are performant for this use case
- **Maintenance:** Consider long-term maintenance and community support

---

### Template Engine Options

**Python:**
- **Jinja2:** Industry standard, Django templates, powerful inheritance
- **Mako:** Fast, Pythonic

**Node.js:**
- **EJS:** Embedded JavaScript, similar to ERB
- **Handlebars:** Logic-less, mustache-based
- **Pug:** Indentation-based, concise

**Go:**
- **html/template:** Built-in, secure by default
- **templ:** Type-safe, component-based

**Recommendation:** Use what integrates best with chosen backend framework

---

### CSS Framework Options

**Option A: Utility-first (Recommended)**
- **Tailwind CSS:** Rapid development, highly customizable
- **UnoCSS:** Instant on-demand atomic CSS

**Option B: Component-based**
- **Bootstrap:** Mature, comprehensive, large bundle
- **Bulma:** Modern, flexbox-based, no JavaScript

**Option C: Minimal/Custom**
- Write custom CSS with modern features (Grid, Flexbox, CSS Variables)
- Use CSS reset/normalize

**Recommendation:** Tailwind CSS for rapid development and consistency with modern practices

---

### JavaScript Approach

**Option A: Minimal Vanilla JS (Recommended for simplicity)**
- Use vanilla JavaScript for interactivity
- Keep bundle size small
- Progressive enhancement

**Option B: Lightweight Libraries**
- **Alpine.js:** Minimal framework for reactivity
- **htmx:** HTML-driven interactions (AJAX, WebSockets)
- **Petite Vue:** Minimal Vue-like reactivity

**Option C: Full Framework (if you need rich interactivity)**
- **React/Vue/Svelte:** Component-based, but adds complexity

**Recommendation:** Start with vanilla JS + htmx for AJAX interactions

---

## 12. Migration Phases

### Phase 1: Foundation & Static Pages
**Goal:** Set up backend templating system and migrate static content

**Tasks:**
1. Choose backend framework and template engine
2. Set up project structure and static file serving
3. Migrate static assets (images, create CSS framework setup)
4. Implement base template with header/footer/layout
5. Migrate Marketing page (`/`) with all animations
6. Migrate About page (`/about`)
7. Set up routing system
8. Test responsive design on multiple devices

**Deliverable:** Marketing and About pages fully functional

---

### Phase 2: Authentication & Session Management
**Goal:** Implement server-side authentication

**Tasks:**
1. Design session management system
2. Implement auth token validation (JWT verification)
3. Create auth modal/page
4. Implement session creation and cookie handling
5. Add CSRF protection
6. Create middleware for protected routes
7. Test authentication flow

**Deliverable:** Users can authenticate and access protected features

---

### Phase 3: Bounty Browsing
**Goal:** Migrate bounty list and search/filter functionality

**Tasks:**
1. Create bounty list template (`/bounties`)
2. Implement server-side data fetching
3. Create bounty card component
4. Implement search form and query handling
5. Implement filter panel (platform, reward, sort)
6. Add empty states
7. Implement recently paid bounties section
8. Add pagination or infinite scroll (if needed)
9. Optional: Add AJAX polling for live updates

**Deliverable:** Fully functional bounty browsing with search/filter

---

### Phase 4: Bounty Details
**Goal:** Migrate bounty detail page

**Tasks:**
1. Create bounty detail template (`/bounties/:id`)
2. Implement server-side Markdown rendering
3. Create info chips/badges
4. Add claim activity timeline
5. Implement claim button (link to claim page/modal)
6. Implement fund button (link to funding page/modal)
7. Add 404 handling for invalid bounty IDs
8. Optional: Add AJAX polling for status updates

**Deliverable:** Fully functional bounty detail page

---

### Phase 5: Bounty Creation
**Goal:** Migrate create bounty flow

**Tasks:**
1. Create bounty creation template/wizard
2. Implement multi-step form or single page form
3. Add requirements editor with "Harden" AJAX call
4. Implement cost calculator (JavaScript)
5. Add form validation (client and server)
6. Implement bounty creation API call
7. Create funding QR page/modal
8. Add QR code generation
9. Implement payment countdown timer
10. Add payment verification polling

**Deliverable:** Users can create bounties with full funding flow

---

### Phase 6: Bounty Claiming
**Goal:** Migrate claim submission flow

**Tasks:**
1. Create claim form template/modal
2. Implement content ID/URL input
3. Add content ID parsing (server-side + optional AJAX)
4. Implement wallet address input with persistence
5. Add form validation
6. Implement claim submission
7. Add success/error handling
8. Show help section with wallet provider links

**Deliverable:** Users can submit bounty claims

---

### Phase 7: Auxiliary Features
**Goal:** Migrate remaining dialogs and features

**Tasks:**
1. Implement contact form
2. Add flash message/toast notification system
3. Implement loading states and spinners
4. Add error pages (404, 500)
5. Implement all remaining modals
6. Add accessibility features (ARIA labels, keyboard navigation)
7. Optimize images and assets
8. Add meta tags for SEO

**Deliverable:** All features fully migrated

---

### Phase 8: Performance & Polish
**Goal:** Optimize and prepare for production

**Tasks:**
1. Add caching headers for static assets
2. Implement CSS/JS minification and bundling
3. Optimize images (WebP, compression, lazy loading)
4. Add CDN configuration
5. Implement monitoring and logging
6. Add analytics (if needed)
7. Perform accessibility audit (WCAG compliance)
8. Cross-browser testing
9. Performance testing (Lighthouse, WebPageTest)
10. Security audit (OWASP checklist)

**Deliverable:** Production-ready application

---

### Phase 9: Deployment & Migration
**Goal:** Deploy and cutover to new system

**Tasks:**
1. Set up production environment
2. Configure domain and SSL
3. Deploy backend with templates
4. Set up monitoring and alerting
5. Run parallel testing (old vs new)
6. Create cutover plan
7. Execute cutover (DNS/routing change)
8. Monitor for issues
9. Deprecate old Flutter app

**Deliverable:** New HTML template system live in production

---

## 13. Key Considerations & Risks

### Performance
- **Benefit:** Reduced initial load time (no large JS bundle)
- **Benefit:** Server-side rendering improves perceived performance
- **Risk:** Server load increases with more users
- **Mitigation:** Implement caching (Redis, CDN), optimize queries

### SEO
- **Benefit:** Server-rendered HTML is fully indexable
- **Benefit:** Better social media previews (Open Graph tags)
- **Risk:** None (significant improvement over SPA)

### Development Complexity
- **Benefit:** Simpler stack, less JavaScript complexity
- **Benefit:** Easier to debug (server-side)
- **Risk:** Team may need to learn backend templating
- **Mitigation:** Choose framework team is familiar with

### User Experience
- **Risk:** Page reloads may feel slower than SPA transitions
- **Mitigation:** Use AJAX for dynamic updates, optimize server response time
- **Risk:** Loss of client-side state on navigation
- **Mitigation:** Use sessions and query parameters to preserve state

### Browser Compatibility
- **Benefit:** Works with JavaScript disabled
- **Benefit:** Better support for older browsers
- **Risk:** Must ensure CSS/JS features are well-supported
- **Mitigation:** Use progressive enhancement, feature detection

### Maintenance
- **Benefit:** Single codebase (backend + frontend)
- **Benefit:** Shared types/models between backend and templates
- **Risk:** Coupling between backend and frontend
- **Mitigation:** Use good separation of concerns (MVC pattern)

### Future Mobile App
- **Risk:** If you need a native mobile app later, you'll need an API
- **Mitigation:** Design backend to support both templates and JSON API responses

---

## 14. Success Metrics

### Performance Metrics
- First Contentful Paint (FCP): < 1.5s
- Largest Contentful Paint (LCP): < 2.5s
- Time to Interactive (TTI): < 3.5s
- Cumulative Layout Shift (CLS): < 0.1
- First Input Delay (FID): < 100ms

### Code Metrics
- Lines of code reduction: Target 30-50% reduction
- Bundle size: JavaScript < 50KB (vs 2MB+ Flutter bundle)
- Number of HTTP requests: < 30 on initial load
- CSS size: < 100KB

### Business Metrics
- User engagement (time on site, pages per session)
- Conversion rates (bounty creation, claims)
- Bounce rate
- Mobile vs desktop usage

### Development Metrics
- Development velocity (features shipped per sprint)
- Bug reports and resolution time
- Developer onboarding time

---

## 15. Next Steps

1. **Choose Technology Stack:** Decide on backend framework, template engine, CSS framework
2. **Set Up Repository:** Create monorepo structure or integrate into existing backend
3. **Prototype Phase 1:** Build marketing page as proof of concept
4. **Review & Iterate:** Get stakeholder feedback on approach
5. **Execute Migration:** Follow phased approach (Phases 1-9)
6. **Launch & Monitor:** Deploy to production and monitor performance

---

## Appendix A: File Structure Example

```
/incentivizethis-backend
  /app
    /routes
      - marketing.py/js/go (marketing page route)
      - bounties.py/js/go (bounty list and detail routes)
      - auth.py/js/go (authentication routes)
      - api.py/js/go (optional JSON API endpoints)
    /templates
      /layouts
        - base.html (base layout with header/footer)
      /pages
        - marketing.html
        - bounties.html
        - bounty_detail.html
        - about.html
        - create_bounty.html
        - claim_bounty.html
        - funding_qr.html
      /components (partials/includes)
        - bounty_card.html
        - info_chip.html
        - filter_panel.html
        - auth_modal.html
        - contact_modal.html
    /static
      /css
        - main.css
        - components.css
      /js
        - main.js
        - polling.js
        - animations.js
      /images
        - [all marketing images]
        - qr-center.png
      /fonts (optional)
    /services
      - bounty_service.py/js/go (business logic)
      - auth_service.py/js/go
      - payment_service.py/js/go
    /models
      - bounty.py/js/go
      - user.py/js/go
    /utils
      - markdown.py/js/go
      - content_id_parser.py/js/go
      - qr_generator.py/js/go
  /config
    - settings.py/js/go (environment-based config)
  /tests
    - [test files]
  - requirements.txt / package.json / go.mod
  - .env
  - README.md
```

---

## Appendix B: Technology Comparison Matrix

| Aspect | Flutter (Current) | HTML Templates (Target) |
|--------|-------------------|-------------------------|
| Initial Load | ~2MB JS bundle | ~50KB JS + HTML |
| Time to First Paint | 2-4s | 0.5-1.5s |
| SEO | Requires SSR workarounds | Native |
| Development Speed | Fast (hot reload) | Fast (template changes) |
| Code Complexity | High (Dart + Flutter) | Medium (HTML + JS + Backend) |
| Browser Compatibility | Modern browsers only | All browsers |
| Offline Support | Good (with PWA) | Limited (requires PWA) |
| Server Load | Low (static hosting) | Medium (server rendering) |
| Maintenance | Two codebases | Single codebase |
| Mobile App Reuse | Limited (web only) | None (need separate app) |

---

## Conclusion

This migration from Flutter to HTML templates will significantly improve performance, SEO, and maintainability while reducing code complexity. The phased approach allows for incremental delivery and risk mitigation. By leveraging server-side rendering and modern HTML/CSS/JS features, you can deliver a fast, accessible, and user-friendly experience while maintaining all existing functionality.

**Recommended First Action:** Set up a minimal prototype of the marketing page to validate the technology stack and approach before committing to full migration.
