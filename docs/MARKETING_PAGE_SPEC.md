# IncentivizeThis Marketing Landing Page Specification

## Overview
A single-page scrolling marketing site with parallax effects, scroll-based animations, and multiple content sections. The page introduces IncentivizeThis, a platform for creating and claiming content bounties. Features a full-screen hero section with background image, followed by alternating content sections with images and text.

## Color Scheme & Theme

### Uses Material Design 3
- **Primary Color**: Blue-based
- **Surface Colors**:
  - `surface` - Default surface color
  - `surfaceContainerHighest` - Elevated surface (alternating sections)
- **Outline Color**: For tertiary buttons
- **Border Radius**: 12-16px throughout

## Page Structure

The page is a full-screen scrollable layout with:
- Fixed parallax background image
- Scrolling content overlaid on top
- 7 major sections (1 hero + 5 content + 1 CTA)
- Maximum content width: 800px (centered)

---

## Section 1: Hero Section

### Background
- **Full-screen parallax background image**: `marketing-carrot.jpg`
  - Parallax effect: Background scrolls at 0.5x speed of content
  - Image fit: Cover with center alignment
  - Always visible behind content

### Content Container
- **Max width**: 800px (centered)
- **Padding**: 150px top, 24px left/right, 24px bottom
- **Height**: Approximately screen height

### Elements (top to bottom):

#### 1. Scroll Indicator Arrow
- **Icon**: Down arrow (`keyboard_arrow_down`)
- **Size**: 48px
- **Color**: White
- **Animation**:
  - Bounces vertically (slides up/down 15% over 1 second, repeats)
  - Fades out as user scrolls (invisible after 100px scroll)
- **Spacing below**: 40px

#### 2. Welcome Message
- **Text**: "Welcome to IncentivizeThis"
- **Font size**: 32px
- **Font weight**: Bold
- **Color**: White
- **Container**:
  - Background: Black at 50% opacity
  - Padding: 20px all sides
  - Border radius: 15px
- **Animation**: Fades out as user scrolls (invisible by 75% of screen height)
- **Spacing below**: 330px

#### 3. Embedded Video Player
- **Video ID**: `ab980b4e-8c41-4562-b04b-5181e798307d` (Clueso video player component)
- **Border radius**: 16px (rounded corners)
- **Aspect ratio**: Maintained by player component
- Always visible (no fade animation)

#### 4. Spacer
- **Height**: Screen height - 250px
- Allows smooth transition to next section

---

## Section 2: "Ads Suck"

### Container
- **Height**: 500px
- **Background**: `surfaceContainerHighest` color
- **Padding**: 24px horizontal, 48px vertical
- **Layout**: Two-column row (50/50 split)

### Animation
- Fades in and slides horizontally as section enters viewport
- Left column: Slides in from left (-50px to 0)
- Right column: Slides in from right (+50px to 0)
- Both reach full opacity simultaneously

### Left Column (Text Content)
**Heading**:
- Text: "Ads Suck."
- Font size: 28px
- Font weight: Bold

**Spacing**: 16px

**Body Text**:
- Text: "Yuck. Nobody's clicking on this. At least not on purpose."
- Font size: 16px
- Line height: 1.5

**Alignment**: Left-aligned, vertically centered

### Middle Spacing
- **Gap**: 24px between columns

### Right Column (Image)
- **Image**: `marketing-yuck-full.png`
- **Border radius**: 16px
- **Fit**: Cover
- **Alignment**: Vertically centered

---

## Section 3: "Just Incentivize Creators!"

### Container
- **Height**: 500px
- **Background**: `surface` color (alternates from previous section)
- **Padding**: 24px horizontal, 48px vertical
- **Layout**: Two-column row (50/50 split)

### Animation
- Fades in and slides horizontally as section enters viewport
- Left column: Slides in from left (-50px to 0)
- Right column: Slides in from right (+50px to 0)

### Left Column (Image)
- **Image**: `marketing-browse.png`
- **Border radius**: 16px
- **Fit**: Cover
- **Alignment**: Vertically centered

### Middle Spacing
- **Gap**: 24px between columns

### Right Column (Text Content)
**Heading**:
- Text: "Just Incentivize Creators!"
- Font size: 28px
- Font weight: Bold
- Alignment: Center

**Spacing**: 16px

**Body Text - Line 1**:
- Text: "Tell us you want:"
- Style: `bodyLarge` (theme)
- Line height: 1.5
- Alignment: Center

**Spacing**: 8px

**Animated Platform Examples** (cycles every 3 seconds):
The following examples rotate with fade/slide transitions:

1. **Reddit Post**: "a Reddit post with at least 1k upvotes mentioning Home Depot"
   - Platform badge: "Reddit" - Background: `rgb(234, 78, 0)` (Orange), White text

2. **Reddit Comment**: "a Reddit comment in r/OrangeCounty about SuzieCakes bakery"
   - Platform badge: "Reddit" - Background: `rgb(234, 78, 0)` (Orange), White text

3. **YouTube Video**: "a YouTube video about Mark Weins visiting Lisbon, Portugal"
   - Platform badge: "YouTube" - Background: `rgb(255, 33, 33)` (Red), White text

4. **YouTube Comment**: "a YouTube comment with at least 100 likes on a video about oysters"
   - Platform badge: "YouTube" - Background: `rgb(255, 33, 33)` (Red), White text

5. **Bluesky Post**: "a Bluesky post about how A.I. doesn't live up to the hype"
   - Platform badge: "Bluesky" - Background: `rgb(45, 165, 245)` (Blue), White text

6. **Instagram Post**: "an Instagram post with at least 2M likes about Positano, Italy"
   - Platform badge: "Instagram" - Background: Purple-to-orange gradient (`#833AB4` to `#F77737`), White text

7. **Twitch Video**: "a Twitch video about Dota 2 with at least 100k views"
   - Platform badge: "Twitch" - Background: `rgb(127, 21, 157)` (Purple), White text

8. **Twitch Clip**: "a Twitch clip from Purge's channel with at least 25k views"
   - Platform badge: "Twitch" - Background: `rgb(127, 21, 157)` (Purple), White text

9. **HackerNews Post**: "a HackerNews post about Temporal with at least 100 upvotes"
   - Platform badge: "HackerNews" - Background: `#FF6600` (Orange), Text: `rgb(250, 239, 227)` (Cream)

10. **HackerNews Comment**: "a HackerNews comment with tips on using Goose"
    - Platform badge: "HackerNews" - Background: `#FF6600` (Orange), Text: `rgb(250, 239, 227)` (Cream)

11. **TripAdvisor Review (Hotel)**: "a TripAdvisor review for a hotel in Honolulu with a 5 star rating"
    - Platform badge: "TripAdvisor" - Background: `rgb(44, 175, 122)` (Green), White text

12. **TripAdvisor Review (Restaurant)**: "a TripAdvisor review for a restaurant in New York"
    - Platform badge: "TripAdvisor" - Background: `rgb(44, 175, 122)` (Green), White text

**Platform Badge Styling**:
- Padding: 8px horizontal, 2px vertical
- Border radius: 8px
- Font: `titleMedium`, bold, line height 1.5
- Inline with surrounding text
- The platform name appears as a styled badge within the sentence

**Animation Details**:
- Duration: 500ms per transition
- Effect: Fade + slide up (starts 25% below, slides to position)
- Curve: Ease in-out
- List is shuffled on page load
- Cycles continuously every 3 seconds

**Spacing**: 8px

**Body Text - Line 2**:
- Text: "and we'll fund bounties that match your niche and audience."
- Style: `bodyLarge` (theme)
- Line height: 1.5
- Alignment: Center

**Column Alignment**: Vertically and horizontally centered

---

## Section 4: "Regular Users Post..."

### Container
- **Height**: 500px
- **Background**: `surfaceContainerHighest` color
- **Padding**: 24px horizontal, 48px vertical
- **Layout**: Two-column row (50/50 split)

### Animation
- Fades in and slides horizontally as section enters viewport
- Left column: Slides in from left (-50px to 0)
- Right column: Slides in from right (+50px to 0)

### Left Column (Text Content)
**Heading**:
- Text: "Regular Users Post..."
- Font size: 28px
- Font weight: Bold

**Spacing**: 16px

**Body Text**:
- Text: "Just 3 upvotes. That's all it took to steer thousands of dollars away from a big-box retailer and into the hands of a local business owner."
- Font size: 16px
- Line height: 1.5

**Alignment**: Left-aligned, vertically centered

### Middle Spacing
- **Gap**: 24px between columns

### Right Column (Image)
- **Image**: `marketing-content.png`
- **Border radius**: 16px
- **Fit**: Cover
- **Alignment**: Vertically centered

---

## Section 5: "...And Get Paid!"

### Container
- **Height**: 500px
- **Background**: `surface` color
- **Padding**: 24px horizontal, 48px vertical
- **Layout**: Two-column row (50/50 split)

### Animation
- Fades in and slides horizontally as section enters viewport
- Left column: Slides in from left (-50px to 0)
- Right column: Slides in from right (+50px to 0)

### Left Column (Image)
- **Image**: `marketing-paid.png`
- **Border radius**: 16px
- **Fit**: Cover
- **Alignment**: Vertically centered

### Middle Spacing
- **Gap**: 24px between columns

### Right Column (Text Content)
**Heading**:
- Text: "...And Get Paid!"
- Font size: 28px
- Font weight: Bold

**Spacing**: 16px

**Body Text**:
- Text: "Anyone can submit their content for review. If it meets the bounty criteria, they get paid instantly with USDC!"
- Font size: 16px
- Line height: 1.5

**Alignment**: Left-aligned, vertically centered

---

## Section 6: Call-to-Action (CTA)

### Container
- **Height**: 500px
- **Background**: `surfaceContainerHighest` color
- **Layout**: Centered column

### Animation
- Fades in and scales/slides vertically as section enters viewport
- Triggers when user scrolls to bottom 500px of page
- Starts at 95% scale, grows to 100%
- Slides up from +50px to 0
- Opacity: 0 to 1

### Content (Vertically Centered)

#### Heading
- **Text**: "Ready to Get Started?"
- **Style**: `headlineMedium` (theme)
- **Font weight**: Bold
- **Spacing below**: 32px

#### Button Row (Wrappable)
- **Layout**: Horizontal wrap with center alignment
- **Spacing**: 20px horizontal, 20px vertical (between wrapped lines)
- **Contains 3 buttons**:

##### 1. "Create a Bounty" (Primary Action)
- **Type**: Elevated button
- **Background**: Primary color
- **Text color**: On-primary color
- **Padding**: 24px horizontal, 16px vertical
- **Font**: `labelLarge`, bold
- **Border radius**: 12px
- **Action**: Opens create bounty dialog (modal)

##### 2. "View Bounties" (Secondary Action)
- **Type**: Outlined button
- **Border**: Primary color, 2px width
- **Text color**: Primary color
- **Padding**: 24px horizontal, 16px vertical
- **Font**: `labelLarge`, bold
- **Border radius**: 12px
- **Action**: Navigate to `/bounties` route

##### 3. "Contact Us" (Tertiary Action)
- **Type**: Outlined button
- **Border**: Outline color, 1px width
- **Text color**: Default
- **Padding**: 24px horizontal, 16px vertical
- **Font**: Default
- **Border radius**: 12px
- **Action**: Opens contact dialog (modal)

---

## Scroll Animations

### Animation Triggers
Sections fade in and slide when they enter the viewport. Each section (2-5) animates independently based on scroll position:

- **Start trigger**: Section begins animating when it's 80% of screen height before entering viewport
- **End trigger**: Animation completes when user has scrolled halfway through the section
- **Opacity range**: 0 to 1
- **Slide range**: -50px or +50px to 0 (depending on column)

### Parallax Background
- Background image scrolls at 50% speed of content
- Creates depth effect as user scrolls
- Image transforms with negative Y offset equal to half the scroll position

### Hero Text Fade
- Welcome message opacity: 1 to 0
- Fade range: 0px scroll to 75% of screen height
- Smooth linear fade

### Arrow Bounce & Fade
- Continuous bounce animation (1 second loop, reverses)
- Fades out: 0-100px scroll (opacity 1 to 0)

---

## Responsive Behavior

### Desktop/Wide Screens
- All sections use two-column layouts (50/50 split)
- Maximum content width: 800px (hero section)
- Images and text side-by-side

### Mobile Considerations
(Note: The current implementation doesn't show explicit mobile breakpoints, but typical Flutter responsive design would:)
- Stack columns vertically on narrow screens
- Reduce font sizes
- Adjust padding
- Single-column layout for content sections

---

## Assets Required

### Images
1. `marketing-carrot.jpg` - Full-screen hero background (carrot image)
2. `marketing-yuck-full.png` - Section 2 illustration (ad example)
3. `marketing-browse.png` - Section 3 illustration (browse bounties UI)
4. `marketing-content.png` - Section 4 illustration (user content example)
5. `marketing-paid.png` - Section 5 illustration (payment confirmation)

### Video
- Clueso video player with video ID: `ab980b4e-8c41-4562-b04b-5181e798307d`

---

## Interactive Elements

### Buttons
- **Create Bounty**: Opens modal dialog
- **View Bounties**: Navigation to bounties page
- **Contact Us**: Opens contact form dialog

### Scroll Interaction
- Pull-to-scroll reveals content
- Parallax background
- Progressive disclosure through scroll-triggered animations
- Smooth fade and slide transitions

---

## Animation Timing

| Element | Duration | Timing Function | Repeat |
|---------|----------|----------------|--------|
| Arrow bounce | 1s | Ease in-out | Infinite (reverse) |
| Platform text rotation | 500ms transition | Ease in-out | Every 3s |
| Section fade-in | Progressive (scroll-based) | Linear | Once |
| Parallax background | Continuous (scroll-based) | Linear | Continuous |
| CTA scale/fade | Progressive (scroll-based) | Linear | Once |

---

## Technical Notes

### Scroll Controller
- Custom scroll listener tracks scroll position
- Calculates animation values for each section independently
- Updates state on scroll for smooth animations

### Platform Examples
- List is shuffled on page load for variety
- Cycles through all 12 examples
- Timer-based rotation (3 seconds)
- Smooth crossfade transition between items

### Animation Formula (Sections 2-5)
```
sectionStart = heroHeight + (sectionNumber - 1) × 500px
animationStart = sectionStart - (screenHeight × 0.8)
animationEnd = sectionStart + 250px
animationValue = (scrollOffset - animationStart) / (animationEnd - animationStart)
animationValue = clamp(animationValue, 0, 1)
```

### CTA Animation
- Triggers in last 500px of scroll
- Starts when user reaches `maxScrollExtent - 500`
- Progresses over final 500px of scroll

---

## Accessibility
- Semantic HTML structure (when converted)
- Sufficient color contrast for all text
- Touch-friendly button sizes (24px × 16px padding minimum)
- Keyboard navigation support
- Screen reader friendly content flow

---

## Content Tone & Messaging

1. **Problem**: Traditional ads are ineffective and annoying
2. **Solution**: Direct incentives for content creators
3. **Process**: Create bounties → Users create content → Get paid
4. **Call-to-action**: Invite users to start creating bounties or browsing existing ones

Simple, direct language with a casual, friendly tone. Emphasis on empowering regular users and creators.
