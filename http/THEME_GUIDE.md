# Theme Guide - IncentivizeThis

This document explains how to use the centralized dark theme system for consistent styling across all components.

## Color Scheme

The application uses a dark theme with the following color palette:

### Background Colors

- **Main Background**: `rgb(16, 20, 24)` - Used for page backgrounds
- **Elevated Background**: `rgb(38, 42, 46)` - Used for dialogs, modals, cards
- **Surface Background**: `rgb(24, 28, 32)` - Used for buttons, inputs, interactive surfaces

### Text Colors

- **Primary Text**: `rgb(159, 201, 253)` - Used for headings, primary button text, important content
- **Secondary Text**: `rgba(255, 255, 255, 0.7)` - Used for body text, labels, descriptions
- **Muted Text**: `rgba(255, 255, 255, 0.5)` - Used for placeholders, disabled text
- **Inverse Text**: `rgb(16, 20, 24)` - Used for text on light backgrounds (rare)

### Accent Colors

- **Accent Blue**: `rgb(159, 201, 253)` - Primary accent color
- **Accent Blue Dark**: `rgb(100, 150, 210)` - Darker variant for hover states

### Button Colors

- **Primary Button Background**: `rgb(160, 202, 252)` - Light blue
- **Primary Button Text**: `rgb(0, 50, 88)` - Dark blue
- **Primary Button Hover**: `rgb(140, 182, 232)` - Slightly darker on hover

### Status Colors

- **Success**: `rgb(76, 175, 80)`
- **Warning**: `rgb(255, 152, 0)`
- **Error**: `rgb(244, 67, 54)`

## Using the Theme

### CSS Variables

All theme colors and properties are available as CSS variables in `/static/css/theme.css`:

```css
:root {
    --bg-main: rgb(16, 20, 24);
    --bg-elevated: rgb(38, 42, 46);
    --bg-surface: rgb(24, 28, 32);
    --text-primary: rgb(159, 201, 253);
    --text-secondary: rgba(255, 255, 255, 0.7);
    --btn-primary-bg: rgb(160, 202, 252);
    --btn-primary-text: rgb(0, 50, 88);
    --btn-primary-hover: rgb(140, 182, 232);
    /* ... and more */
}
```

### Using CSS Variables in HTML

```html
<!-- Inline styles -->
<div style="background-color: var(--bg-elevated); color: var(--text-primary);">
    Content
</div>

<!-- Or use utility classes (preferred) -->
<div class="bg-elevated text-primary">
    Content
</div>
```

### Using CSS Variables in CSS

```css
.my-component {
    background-color: var(--bg-elevated);
    color: var(--text-secondary);
    border: 1px solid var(--border-default);
    border-radius: var(--radius-lg);
    padding: var(--spacing-md);
}

.my-component:hover {
    border-color: var(--border-focus);
}
```

### Using Tailwind with Theme Colors

Tailwind is configured in `base.html` with custom colors that match our theme:

```html
<!-- Use brand colors -->
<div class="bg-brand-bg-elevated text-brand-text-primary">
    Content
</div>

<!-- Use primary colors (same as accent blue) -->
<button class="bg-primary text-white">
    Button
</button>
```

## Component Patterns

### Buttons

```html
<!-- Primary Button -->
<button class="btn btn-primary">
    Primary Action
</button>

<!-- Secondary Button -->
<button class="btn btn-secondary">
    Secondary Action
</button>

<!-- Custom styled with CSS variables -->
<button style="background-color: var(--bg-surface); color: var(--text-primary);"
        class="px-6 py-3 rounded-lg font-bold hover:opacity-90 transition-all">
    Custom Button
</button>
```

### Cards/Dialogs

```html
<!-- Using utility class -->
<div class="card">
    Card content
</div>

<!-- Using inline styles -->
<div style="background-color: var(--bg-elevated);"
     class="rounded-2xl p-6 shadow-2xl">
    Dialog content
</div>
```

### Form Inputs

```html
<div>
    <label class="label" for="input-id">Label Text</label>
    <input type="text"
           id="input-id"
           class="input"
           placeholder="Placeholder text">
</div>

<!-- Or with inline styles for custom behavior -->
<input type="text"
       class="w-full px-4 py-2 rounded-lg focus:outline-none transition-all"
       style="background-color: var(--bg-surface);
              color: var(--text-secondary);
              border: 1px solid var(--border-default);"
       onfocus="this.style.borderColor='var(--border-focus)'"
       onblur="this.style.borderColor='var(--border-default)'">
```

### Status Messages

```html
<!-- Success Message -->
<div class="message-success">
    ✓ Operation completed successfully!
</div>

<!-- Error Message -->
<div class="message-error">
    ✗ An error occurred.
</div>

<!-- Warning Message -->
<div class="message-warning">
    ⚠ Please review this information.
</div>
```

### Links

```html
<a href="/path" class="link">
    Click here
</a>
```

### Badges/Chips

```html
<span class="badge" style="background-color: var(--accent-blue); color: var(--bg-main);">
    Badge Text
</span>
```

## Best Practices

### 1. Prefer CSS Variables Over Hard-Coded Colors

❌ **Bad:**
```html
<div style="background-color: rgb(38, 42, 46); color: rgb(159, 201, 253);">
```

✅ **Good:**
```html
<div style="background-color: var(--bg-elevated); color: var(--text-primary);">
```

### 2. Use Utility Classes When Available

❌ **Bad:**
```html
<div style="background-color: var(--bg-elevated); border-radius: var(--radius-2xl); padding: var(--spacing-xl);">
```

✅ **Good:**
```html
<div class="card">
```

### 3. Maintain Consistent Spacing

Use the spacing variables for consistency:
- `--spacing-xs` (0.25rem)
- `--spacing-sm` (0.5rem)
- `--spacing-md` (1rem)
- `--spacing-lg` (1.5rem)
- `--spacing-xl` (2rem)
- `--spacing-2xl` (3rem)

### 4. Use Transitions for Interactive Elements

Always add smooth transitions to interactive elements:

```css
.my-button {
    transition: all var(--transition-base);
}

.my-button:hover {
    opacity: 0.9;
    transform: translateY(-1px);
}
```

### 5. Maintain Color Hierarchy

- **Backgrounds**: Main < Surface < Elevated
- **Text**: Muted < Secondary < Primary
- Use primary text color for headings and important content
- Use secondary text for body text and labels
- Use muted text for placeholders and less important info

## Adding New Components

When creating new components:

1. **Use existing CSS variables** from theme.css
2. **Follow the established patterns** for buttons, cards, inputs
3. **Add reusable styles** to theme.css if needed
4. **Test in both light and dark contexts** (though we primarily use dark)
5. **Ensure accessibility** - maintain sufficient contrast ratios

## Modifying the Theme

If you need to change colors across the entire application:

1. Update the values in `theme.css` `:root` selector
2. Update the Tailwind config in `base.html` to match (for Tailwind utility classes)
3. All components using CSS variables will automatically update

## Example: Complete Form

```html
<form class="space-y-4">
    <div>
        <label class="label" for="name">Name</label>
        <input type="text"
               id="name"
               class="input"
               placeholder="Enter your name">
    </div>

    <div>
        <label class="label" for="email">Email</label>
        <input type="email"
               id="email"
               class="input"
               placeholder="you@example.com">
    </div>

    <div>
        <label class="label" for="message">Message</label>
        <textarea id="message"
                  class="textarea"
                  rows="4"
                  placeholder="Your message here..."></textarea>
    </div>

    <div class="flex gap-3">
        <button type="submit" class="btn btn-primary flex-1">
            Submit
        </button>
        <button type="button" class="btn btn-secondary">
            Cancel
        </button>
    </div>
</form>
```

## Accessibility Notes

- All interactive elements should have `:focus-visible` styles (handled globally in theme.css)
- Maintain contrast ratio of at least 4.5:1 for normal text
- Use semantic HTML elements
- Provide aria-labels for icon-only buttons
- Ensure keyboard navigation works properly
