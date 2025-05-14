package abb

// --- Bluesky Structures Removed (Definitions now exist in activity.go) ---
/*
type BlueskyDependencies struct { ... }
func (deps BlueskyDependencies) Type() PlatformKind { ... }
func (deps BlueskyDependencies) MarshalJSON() ([]byte, error) { ... }
func (deps *BlueskyDependencies) UnmarshalJSON(data []byte) error { ... }
type BlueskyContent struct { ... }
type BlueskyProfileViewBasic struct { ... }
type BlueskyEmbedView struct { ... }
type BlueskyEmbedImageView struct { ... }
type BlueskyEmbedExternalView struct { ... }
type BlueskyEmbedRecordView struct { ... }
type BlueskyHandleResponse struct { ... }
*/

// --- Removed ResolveBlueskyURLToATURI function ---
/*
func (a *Activities) ResolveBlueskyURLToATURI(ctx context.Context, postURL string) (string, error) {
    // ... logic moved to PullContentActivity ...
}
*/

// --- Removed PullBlueskyContent function ---
/*
func (a *Activities) PullBlueskyContent(ctx context.Context, contentID string, contentKind ContentKind) (*BlueskyContent, error) {
    // ... logic moved to PullContentActivity ...
}
*/
