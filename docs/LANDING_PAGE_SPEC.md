# IncentivizeThis Landing Page Specification

## Overview
The landing page is a bounty platform where users can view, filter, and create content bounties across various platforms. The design uses Material Design 3 principles with a centered, constrained layout (max-width: 600px) for optimal readability.

## Color Scheme

### Light Theme
- **Primary Color**: Blue-based Material 3 color scheme
- **Surface**: White/Light background
- **Cards**: Flat design (elevation: 0) with light gray borders (grey.shade300)
- **Border Radius**: 12-16px for cards and buttons

### Dark Theme
- **Primary Color**: Blue-based Material 3 color scheme
- **Surface**: Dark background
- **Cards**: Flat design with dark gray borders (grey.shade800)
- **Border Radius**: 12-16px for cards and buttons

### Accent Colors
- **Green (shade700)**: Monetary amounts/funded bounties
- **Orange (shade700)**: Content type indicators
- **Grey (shade600)**: Unfunded status
- **Secondary**: Inventory/remaining posts indicators
- **Tertiary**: Platform indicators

## Page Layout

### 1. Header/AppBar

**Structure**: Fixed header with transparent background, 0 elevation

**Left Section**:
- Carrot emoji icon: 🥕 (24px, primary color)
- 8px spacing
- App title: "IncentivizeThis" (or "Search Results" when searching)
  - Font: Default app title style
  - Truncates with ellipsis if needed

**Right Section** (when NOT searching):
- **Create Bounty Button**:
  - Circular background (primary color)
  - White "+" icon (18px)
  - Tooltip: "Create Bounty"
  - Padding: 4px

- **Info Button**:
  - Outlined info icon (22px, primary color)
  - Tooltip: "About"
  - Links to about page

**Actions Section** (always visible):
- **Search Icon**: Opens search modal
- **Clear Search Icon**: Only visible when search is active
- **Refresh Icon**: Reloads data
- **Filter Icon**:
  - Shows `filter_list` normally
  - Shows `filter_list_off_outlined` when filters are active
  - Opens filter bottom sheet

All action buttons use primary color and disable when loading.

### 2. Main Content Area

The content is wrapped in a `RefreshIndicator` (pull-to-refresh) and uses a scrollable layout with maximum width constraint of 600px, centered.

#### Loading State
- **Centered Column**:
  - Animated circular progress indicator (primary color, 3px stroke)
  - Animation: Scale from 0.8 to 1.0 over 1 second
  - 16px spacing
  - Message text: "Loading bounties..." or "Searching for [query]..."
    - Font: bodyMedium
    - Color: onSurface at 70% opacity

#### Error State
- **Centered Column**:
  - Error icon (64px, error color)
  - 16px spacing
  - "Error" heading (headlineSmall, error color, bold)
  - 8px spacing
  - Error message (bodyMedium, onSurface at 70% opacity)
  - 24px spacing
  - "Try Again" button with refresh icon (elevated, primary background)

#### Empty States

**No Results for Search**:
- Icon: `search_off_rounded` (64px, outline color)
- 16px spacing
- Heading: "No Results for '[query]'" (headlineSmall, bold)
- 8px spacing
- Message: "Try a different search term or clear the search."
- "Clear Search" button (secondary background)

**No Matching Bounties (filters active)**:
- Icon: `filter_alt_off_outlined` (64px, outline color)
- 16px spacing
- Heading: "No Matching Bounties" (headlineSmall, bold)
- 8px spacing
- Message: "Try adjusting your filters or clear them to see all available bounties."
- "Clear Filters" button (secondary background)

**No Bounties Available**:
- Icon: `layers_clear_outlined` (64px, outline color)
- 16px spacing
- Heading: "No Bounties Available" (headlineSmall, bold)
- 8px spacing
- Message: "There are no active or recently paid bounties to display right now. Pull down to refresh."

### 3. Active Bounties Section

**Section Header** (not shown when searching):
- Padding: 16px left/right, 16px top, 8px bottom
- Text: "Active Bounties"
- Style: titleLarge, fontWeight 600, primary color

**Search Results Header** (shown when searching):
- Padding: 16px left/right, 16px top, 0px bottom
- Text: "Search Results for '[query]':"
- Style: titleMedium, onSurface at 80% opacity

#### Bounty Card Layout

Each bounty is displayed as a card with the following structure:

**Card Container**:
- Margin: 16px horizontal, 8px vertical
- Elevation: 0
- Border Radius: 16px
- Border: 1px solid (grey.shade300 light / grey.shade800 dark)
- Padding: 16px all sides
- Clickable with ink ripple effect (primary color at 10% opacity)

**Card Header Row**:
- Space-between layout
- **Left**: Bounty Title
  - Font: titleMedium (small screens < 600px) or titleLarge (larger screens)
  - Weight: Bold
  - Color: onSurface
  - Max lines: 2
  - Overflow: ellipsis
- **Right**: Status Chip
  - See Info Chip specification below

**Spacing**: 12px

**Card Info Row**:
- Wrappable chip layout
- Horizontal spacing: 8px
- Vertical spacing: 4px (between wrapped lines)

**Info Chips** (in order):
1. **Reward Amount**:
   - Icon: `monetization_on_outlined` (16px)
   - Text: "$XX.XX" or "Unfunded"
   - Color: Green (shade700) if funded, Grey (shade600) if unfunded

2. **Platform**:
   - Icon: Platform-specific icon (16px)
   - Text: Platform name (e.g., "Twitter", "YouTube")
   - Color: Tertiary

3. **Content Type**:
   - Icon: `article_outlined` (16px)
   - Text: Content kind (e.g., "Post", "Video")
   - Color: Orange (shade700)

4. **Remaining Posts**:
   - Icon: `inventory_2_outlined` (16px)
   - Text: "X remaining" or "Unlimited"
   - Color: Secondary

5. **Tier Badge** (if tier != 8):
   - Icon: Tier-specific icon
   - Text: Tier name
   - Colors: Tier-specific background and text colors

**Info Chip Specification**:
- Container padding: 10px horizontal, 5px vertical
- Border radius: 16px
- Background: Color at 10% opacity (or custom)
- Content: Icon (16px) + 5px spacing + Text
- Text style: labelMedium, fontWeight 600

**Fund Bounty Button** (only for "AwaitingFunding" status):
- Alignment: Right
- Top margin: 16px
- Icon: `qr_code_scanner`
- Label: "Fund Bounty"
- Style: Elevated button, primary background, onPrimary text
- Opens funding QR dialog

### 4. Recently Paid Section

**Section Header**:
- Padding: 16px left/right, 24px top, 8px bottom
- Text: "Recently Paid"
- Style: titleLarge, fontWeight 600, secondary color
- **Note**: Hidden during search mode

**Paid Bounty Items**:
- List tiles with the following structure:
  - **Leading**:
    - Circular avatar (secondary color at 10% opacity)
    - Receipt icon: `receipt_long` (20px, secondary color)
  - **Title**:
    - Formatted amount (e.g., "$50.00")
    - Style: bodyLarge, fontWeight 600
  - **Subtitle**:
    - Formatted timestamp (e.g., "2 hours ago")
    - Style: bodySmall, onSurface at 60% opacity
  - **Padding**: 20px horizontal, 4px vertical

Shows last 5 recently paid bounties.

### 5. Filter Bottom Sheet

**Structure**: Modal bottom sheet with rounded top corners (20px radius)

**Header**:
- Padding: 20px all sides, plus bottom inset
- Title: "Filter Bounties" (headlineSmall)
- 16px spacing

**Sort By Section**:
- Label: "Sort By" (titleMedium)
- 8px spacing
- Choice chips (wrap layout, 8px spacing horizontal, 4px vertical):
  - "Newest" (default)
  - "Ending Soon"
  - "Highest Reward (Per Post)"
  - "Highest Reward (Total)"
- Selected chip: primaryContainer background, onPrimaryContainer text, bold
- Unselected chip: onSurfaceVariant text

**Platform Section**:
- 24px top spacing
- Label: "Platform" (titleMedium)
- 8px spacing
- Filter chips (wrap layout, 8px spacing):
  - One chip per available platform
  - Shows checkmark when selected
  - Selected: primaryContainer background, onPrimaryContainer checkmark

**Reward Section**:
- 24px top spacing
- Label: "Reward" (titleMedium)
- 8px spacing
- Choice chips (wrap layout, 8px spacing):
  - "Any Reward"
  - "< $10"
  - "$10 - $50"
  - "$50 - $100"
  - "> $100"
- Selected chip: primaryContainer background, onPrimaryContainer text, bold

**Actions**:
- 24px top spacing
- Right-aligned row with 8px spacing:
  - "Reset All" text button
  - "Apply Filters" elevated button

### 6. Search Bottom Sheet

**Structure**: Modal bottom sheet with rounded top corners (20px radius), transparent background

**Container**:
- Background: cardColor (theme-based)
- ClipRRect with 20px top radius
- Contains search input form and submission logic

## Responsive Behavior

### Breakpoints
- **Very Small**: < 450px
- **Small**: < 600px
- **Medium/Large**: >= 600px

### Responsive Adjustments
- Bounty card titles use `titleMedium` on small screens, `titleLarge` on larger screens
- Maximum content width constrained to 600px on all screen sizes
- Content is centered horizontally
- Touch targets maintain minimum size for mobile

## Interactions

### Polling Behavior
- Auto-refreshes every 5 seconds (when not searching)
- Shared timer across all instances
- Pauses during active search
- Silently updates in background (no loading indicator for polls)

### Pull-to-Refresh
- Swipe down on main content to manually refresh
- Shows primary-colored refresh indicator

### Card Tap
- Navigate to bounty detail page
- Passes bounty object as route data

### Create Bounty
- Opens modal dialog
- Remains on home screen

### Search
- Opens bottom sheet modal
- Pauses auto-polling during search
- Shows search results with clear button
- Clearing search resumes auto-polling

### Filter
- Opens bottom sheet modal
- Preview changes before applying
- Can reset all filters at once
- Applies filters and reloads from backend

## State Indicators

### Loading
- Initial load: Full-screen loading indicator
- Background refresh: Silent update (no indicator)
- Search: "Searching for..." message

### Search Active
- Title changes to "Search Results"
- Clear button appears in actions
- Active bounties section header hidden
- Recently paid section hidden
- Auto-polling paused

### Filters Active
- Filter icon changes to `filter_list_off_outlined`
- Can be cleared via filter sheet or dedicated "Clear Filters" button

## Content Specifications

### Text Overflow
- Titles: Max 2 lines, ellipsis
- App title: Single line, ellipsis

### Spacing
- Card margins: 16px horizontal, 8px vertical
- Card padding: 16px all sides
- Section padding: 16px horizontal
- Vertical spacing between elements: 8-24px depending on context

### Icons
- Action icons: 18-24px
- Info chip icons: 16px
- Empty state icons: 48-64px

### Animations
- Card tap: Ink ripple (primary at 10% opacity)
- Loading indicator: Scale animation (0.8 to 1.0 over 1s)
- Page transitions: Default Material navigation

## Accessibility
- All buttons have tooltips
- Color contrast meets WCAG AA standards
- Touch targets minimum 48x48dp
- Screen reader friendly text labels
- Keyboard navigation support (web)
