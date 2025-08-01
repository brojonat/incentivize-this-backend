# Adding a New Platform Provider

This guide outlines the steps required to add a new platform data provider (e.g., Steam) to the Affiliate Bounty Board.

### Todo List

- [ ] **Define Platform and Content Kinds**
- [ ] **Create the Provider Activity File**
- [ ] **Implement Platform Dependencies**
- [ ] **Define Content Structs**
- [ ] **Implement Content Fetching Logic**
- [ ] **Integrate into the `PullContentActivity`**
- [ ] **Update Bounty Creation Handler**
- [ ] **Update LLM Prompt for Content Kind Inference**

---

### Detailed Steps

#### 1. Define Platform and Content Kinds

First, you need to define a new `PlatformKind` for your provider and any new `ContentKind`s it introduces.

- **File**: `abb/activity.go` (or wherever `PlatformKind` is defined).
- **Action**: Add a new `PlatformKind` constant.

  ```go
  // Example for Steam
  const (
      // ... existing platforms
      PlatformSteam PlatformKind = "steam"
  )
  ```

- If your platform has unique content types, add them as well.

  ```go
  // Example for a Steam Review
  const (
      // ... existing content kinds
      ContentKindReview ContentKind = "review" // Note: "review" may already exist
  )
  ```

#### 2. Create the Provider Activity File

Create a new file in the `abb/` directory for your provider's logic.

- **Action**: Create `abb/activity_steam.go`.

#### 3. Implement Platform Dependencies

This struct holds configuration for your provider, like API keys. It needs to conform to the `PlatformDependencies` interface (which is implicitly defined by its usage).

- **File**: `abb/activity_steam.go`
- **Action**: Define your dependencies struct.

  ```go
  package abb

  import (
      "encoding/json"
      "fmt"
  )

  // SteamDependencies holds dependencies for Steam activities.
  type SteamDependencies struct {
      APIKey string `json:"api_key"`
  }

  // Type returns the platform type for Steam.
  func (deps SteamDependencies) Type() PlatformKind {
      return PlatformSteam
  }

  // MarshalJSON implements json.Marshaler for SteamDependencies.
  func (deps SteamDependencies) MarshalJSON() ([]byte, error) {
      // see abb/activity_twitch.go for a good example
  }

  // UnmarshalJSON implements json.Unmarshaler for SteamDependencies.
  func (deps *SteamDependencies) UnmarshalJSON(data []byte) error {
      // see abb/activity_twitch.go for a good example
  }
  ```

  _See `abb/activity_twitch.go` or `abb/activity_tripadvisor.go` for implementation examples of `MarshalJSON` and `UnmarshalJSON`._

#### 4. Define Content Structs

Create Go structs that match the structure of the JSON response from your provider's API.

- **File**: `abb/activity_steam.go`
- **Action**: Define your content struct(s).

  ```go
  // SteamReviewContent represents a user review on Steam.
  type SteamReviewContent struct {
      RecommendationID string `json:"recommendationid"`
      Author           struct {
          SteamID    string `json:"steamid"`
          NumGames   int    `json:"num_games_owned"`
          NumReviews int    `json:"num_reviews"`
      } `json:"author"`
      Language      string `json:"language"`
      Review        string `json:"review"`
      Timestamp     int64  `json:"timestamp_created"`
      VotedUp       bool   `json:"voted_up"`
      // ... other fields
  }
  ```

#### 5. Implement Content Fetching Logic

Write the function that will call the provider's API to get the content. This function will be called from the main `PullContentActivity`.

- **File**: `abb/activity_steam.go`
- **Action**: Add a function to fetch content.

  ```go
  import "context"

  // fetchSteamReview fetches a specific review from the Steam API.
  func (a *Activities) fetchSteamReview(ctx context.Context, deps SteamDependencies, reviewID string) (*SteamReviewContent, error) {
      // Implementation to call the Steam API using deps.APIKey
      // and return the review content or an error.
      return nil, nil // Placeholder
  }
  ```

#### 6. Integrate into `PullContentActivity`

The `PullContentActivity` in `abb/activity.go` acts as a dispatcher. You need to add a case for your new platform.

- **File**: `abb/activity.go`
- **Action**: Modify `PullContentActivity` to handle the new `PlatformKind`.

  ```go
  // Inside PullContentActivity's switch statement for input.Platform
  case PlatformSteam:
      var deps SteamDependencies
      if err := json.Unmarshal(rawDeps, &deps); err != nil {
          return nil, fmt.Errorf("failed to unmarshal steam dependencies: %w", err)
      }

      switch input.ContentKind {
      case ContentKindReview:
          content, err := a.fetchSteamReview(ctx, deps, input.ContentID)
          if err != nil {
              return nil, err
          }
          return json.Marshal(content)
      default:
          return nil, fmt.Errorf("unsupported content kind for steam: %s", input.ContentKind)
      }
  ```

#### 7. Update Bounty Creation Handler

The API needs to be aware of the new provider to accept it during bounty creation.

- **File**: `http/handlers_bounty.go`
- **Actions**:

  1.  Add your new `PlatformKind` to the `validPlatformKinds` slice inside `handleCreateBounty`.

      ```go
      // In handleCreateBounty...
      validPlatformKinds := []abb.PlatformKind{
          // ... existing platforms
          abb.PlatformSteam,
      }
      ```

  2.  Add a `case` for your new platform to the validation `switch` to define its valid `ContentKind`s.

      ```go
      // In handleCreateBounty's platform validation switch...
      switch normalizedPlatform {
      // ... existing cases
      case abb.PlatformSteam:
          if normalizedContentKind != abb.ContentKindReview {
              writeBadRequestError(w, fmt.Errorf("invalid content_kind for Steam: must be '%s'", abb.ContentKindReview))
              return
          }
      // ...
      }
      ```

  3.  Update the `default` case in the same `switch` to include your new platform in the error message.

#### 8. Update LLM Prompt for Content Kind Inference

To help the LLM automatically determine the platform and content type from a bounty's requirements, you need to update its instructions.

- **File**: `http/handlers_bounty.go`
- **Action**: Find the `infer_content_parameters` tool schema in `handleCreateBounty` and update the `description` for `ContentKind` to include your new platform and its supported content kinds.

  ```go
  // In handleCreateBounty, inside the schema for `infer_content_parameters`
  "description": `The kind of content that the bounty is for. This is platform dependent. The valid options are:
  - Reddit: post, comment
  - YouTube: video, comment
  - Twitch: video, clip
  - Hacker News: post, comment
  - Bluesky: post
  - Instagram: post
  - IncentivizeThis: bounty
  - TripAdvisor: review
  - Steam: review`, // Add your new platform here
  ```
