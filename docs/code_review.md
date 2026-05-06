# Code Review Report: Grafana Codebase Analysis

**Date**: 2026-05-06  
**Scope**: Systematic analysis of Go, TypeScript, and TypeScript/React code  
**Status**: Complete

## Executive Summary

This review identifies actionable issues across the Grafana codebase including documented bugs, type safety concerns, incomplete implementations, and code quality improvements. The codebase is well-structured with established patterns, but contains several technical debt items that warrant attention.

**Total Issues Found**: 15  
- Critical: 2
- High: 5
- Medium: 6
- Low: 2

---

## Issues by Category

### Critical Issues

#### 1. Concurrent Request Batching Not Implemented (Performance Bug)
**Location**: `public/app/features/alerting/unified/hooks/useRuleSourcesWithRuler.ts`  
**Severity**: Critical  
**Category**: Performance/Bug

**Description**:
The `useRuleSourcesWithRuler` hook fires all buildinfo requests simultaneously instead of batching them in groups of 10. The test at line 115-145 in `useRuleSourcesWithRuler.test.tsx` explicitly documents this bug:
- Expected behavior: batches of ≤10 concurrent requests
- Current behavior: all 25 requests fire simultaneously (peakConcurrentCount === 25)

This can cause performance degradation when checking ruler support for many datasources.

**Recommendation**:
Implement request batching using a queue-based approach:
1. Collect all datasource UIDs that need checking
2. Process in batches of 10 using `Promise.all()`
3. Wait for each batch to complete before starting the next

**Test Status**: Test marked as "EXPECTED TO FAIL against unmodified hook"

---

#### 2. Loading State Not Properly Tracked During Async Operations
**Location**: `public/app/features/alerting/unified/hooks/useRuleSourcesWithRuler.ts`  
**Severity**: Critical  
**Category**: Bug

**Description**:
The `isLoading` state becomes false before all datasources have resolved their buildinfo. The test at line 147-170+ documents this:
- When datasources are processed in order where slow datasources come first alphabetically
- The `isLoading` flag only reflects the last-triggered lazy query
- Premature `isLoading = false` causes the UI to show incomplete results

This causes users to see incomplete rule source lists before all requests have completed.

**Recommendation**:
Track loading state based on all active queries, not just the last one:
1. Maintain a Set/counter of in-flight queries
2. Update `isLoading` to reflect `activeQueryCount > 0`
3. Ensure cleanup when queries complete

**Test Status**: Test marked as "BUG: isLoading goes false before all datasources have resolved"

---

### High Severity Issues

#### 3. Type Safety: Excessive Use of `any` Type
**Location**: Multiple files across `packages/` and `public/app/`  
**Severity**: High  
**Category**: Type Safety

**Description**:
95+ instances of `any[` pattern found, indicating typed array declarations being used as escape hatches:
```typescript
// Bad examples found in codebase
const data: any[] = ...;
const items: Record<string, any> = ...;
```

This bypasses TypeScript's type safety and makes refactoring unsafe.

**Files Affected**:
- `packages/grafana-api-clients/` (generated code)
- `packages/grafana-ui/`
- `public/app/features/`

**Recommendation**:
1. Audit `any[]` usages (exclude auto-generated files in `*gen.ts`)
2. Replace with specific types where possible
3. Create shared utility types instead of `any`
4. Document unavoidable `any` types with `@ts-expect-error` and comments

**Example Fix**:
```typescript
// Before
const items: any[] = [];

// After
interface Item {
  id: string;
  name: string;
  // ... specific properties
}
const items: Item[] = [];
```

---

#### 4. Dashboard Link Type Generation Issue
**Location**: `packages/grafana-api-clients/src/clients/rtkq/dashboard/v2beta1/endpoints.gen.ts:1152`  
**Severity**: High  
**Category**: Code Generation

**Description**:
API client generator produces incorrect union type for `DashboardLinkType`:
```typescript
// Generated as (WRONG):
type: DashboardLinkType | dashboardLinkType.Link;

// Should be (CORRECT):
type: DashboardLinkType
```

The double representation causes confusion and potential runtime errors.

**Recommendation**:
1. Investigate schema definition in `apps/dashboard/` API specs
2. Fix the OpenAPI/CUE schema definition for DashboardLink
3. Regenerate clients with corrected schema
4. Add integration test to prevent regression

**Related Files**:
- `packages/grafana-schema/src/schema/dashboard/v2beta1/types.spec.gen.ts:1462`
- `packages/grafana-schema/src/schema/dashboard/v2/types.spec.gen.ts:1461`

---

#### 5. Admission Hook Validation Missing
**Location**: `packages/grafana-api-clients/src/clients/rtkq/provisioning/v0alpha1/endpoints.gen.ts:1630, 1646, 1670, 1676`  
**Severity**: High  
**Category**: Security/Validation

**Description**:
Multiple provisioning endpoints marked with `FIXME: we should validate this in admission hooks`:
- Paths to be deleted (multiple references)
- Target branch for export (git provisioning)
- Prefix in target file system

These lack input validation that should prevent invalid/malicious provisioning requests.

**Recommendation**:
1. Implement admission webhook validators in the provisioning app (`apps/`)
2. Validate path traversal attacks (e.g., `../../../etc/passwd`)
3. Validate git ref names and paths
4. Add test cases for each validation rule

**Example Validation**:
```go
// In provisioning app admission hook
func validatePaths(paths []string) error {
  for _, path := range paths {
    if strings.Contains(path, "..") {
      return fmt.Errorf("path traversal detected: %s", path)
    }
  }
  return nil
}
```

---

#### 6. Prometheus String Literal Support Incomplete
**Location**: `packages/grafana-prometheus/src/components/monaco-query-field/monaco-completion-provider/situation.ts:97, 121`  
**Severity**: High  
**Category**: Feature Completeness

**Description**:
The Prometheus query editor doesn't support all valid string literal formats per Prometheus documentation:
- Line 97: `// FIXME: support https://prometheus.io/docs/prometheus/latest/querying/basics/#string-literals`
- Line 121: `throw new Error('FIXME: invalid string literal');`

This breaks autocompletion and validation for valid PromQL expressions containing string literals.

**Recommendation**:
1. Review Prometheus string literal specification
2. Implement parser support for:
   - Double-quoted strings
   - Single-quoted strings
   - Backtick literals (if supported)
   - Escape sequence handling
3. Update completion provider with tests
4. Add validation in MonacoQueryField

---

### Medium Severity Issues

#### 7. Duplicated Browse Dashboard Types
**Location**: `packages/grafana-test-utils/src/types/browse-dashboards.ts:1`  
**Severity**: Medium  
**Category**: Code Duplication

**Description**:
File marked with `// FIXME: This file is a duplication of types within the core code`

Having duplicate type definitions creates maintenance burden and inconsistency risks.

**Recommendation**:
1. Identify the source types (likely in `public/app/types/`)
2. Remove `browse-dashboards.ts`
3. Re-export from the source location:
```typescript
export type { BrowseDashboardsState, BrowseDashboardsAction } from 'app/types/browse-dashboards';
```
4. Update all imports to use the canonical location

---

#### 8. Modal Title Accessibility Issue
**Location**: `packages/grafana-ui/src/components/Modal/Modal.tsx:80`  
**Severity**: Medium  
**Category**: Accessibility

**Description**:
Comment states: `// FIXME: custom title components won't get an accessible title.`

Custom title components bypass the `aria-labelledby` association, breaking screen reader accessibility.

**Recommendation**:
1. Require title consumers to pass both a component and a string ID
2. Render the string ID for accessibility hooks
3. Update Modal API:
```typescript
interface ModalProps {
  title?: React.ReactNode;
  titleId?: string; // Required if title is custom component
}
```
4. Add accessibility tests using testing-library's `getByRole('dialog')`

---

#### 9. PromQL Parsing Limitations
**Location**: `packages/grafana-prometheus/src/querybuilder/parsing.ts:120, 147, 155, 177`  
**Severity**: Medium  
**Category**: Feature Completeness

**Description**:
Multiple TODOs indicate incomplete PromQL parsing:
- Line 120: "When visual query editor support for the 'info' function is implemented"
- Line 147: "There are probably cases where we will just skip nodes we don't support"
- Line 155: "This should be already handled in case parent is binary expression"
- Line 177: "Revisit this function"

This means certain valid PromQL queries won't parse correctly in the visual editor.

**Recommendation**:
1. Build a comprehensive PromQL compliance matrix
2. Add tests for currently unsupported operators/functions
3. Track support for each PromQL feature
4. Prioritize based on usage analytics

---

#### 10. Hardcoded Query Type Endpoints
**Location**: `packages/grafana-data/src/types/featureToggles.gen.ts:297`  
**Severity**: Medium  
**Category**: Configuration

**Description**:
Feature toggle comment indicates: "Show query type endpoints in datasource API servers (currently hardcoded for testdata, expressions, and prometheus)"

Hardcoding datasource types reduces flexibility for adding new query types.

**Recommendation**:
1. Move hardcoded types to a configuration file
2. Allow dynamic registration of query types via plugins
3. Consider Kubernetes CRD-based configuration
4. Add feature toggle documentation

---

#### 11. Incomplete API Client Placeholder
**Location**: `packages/grafana-api-clients/src/index.ts`  
**Severity**: Medium  
**Category**: Code Organization

**Description**:
Comment at top: `/* @TODO figure out how to automatically set the MockBackendSrv when consumers of this package write tests using the exported clients */`

Test consumers can't properly mock backend calls, requiring workarounds in test code.

**Recommendation**:
1. Implement a test export that includes mock setup
2. Create testing utilities in `packages/grafana-api-clients/src/testing/`
3. Export mock factory functions for consumers
4. Document with example

---

#### 12. Radial Gauge Component Needs Coverage
**Location**: `packages/grafana-ui/src/components/RadialGauge/utils.ts:171`  
**Severity**: Medium  
**Category**: Testing

**Description**:
Comment: `// FIXME: needs coverage`

Critical UI component calculations lack test coverage, risking regressions.

**Recommendation**:
1. Write unit tests for all RadialGauge utility functions
2. Test edge cases (0 values, negative values, extreme ranges)
3. Add visual regression tests
4. Target 90%+ code coverage

---

#### 13. Radial Gauge Filter Context Architecture
**Location**: `packages/grafana-ui/src/components/RadialGauge/RadialGauge.tsx:150`  
**Severity**: Medium  
**Category**: Architecture

**Description**:
Comment: `// FIXME: I want to move the ids for these filters into a context which the children`

Current implementation passes filter IDs directly; moving to context would reduce prop drilling.

**Recommendation**:
1. Create `RadialGaugeContext` for filter state
2. Use context to avoid passing IDs through component tree
3. Update child components to consume context
4. Add tests for context consumers

---

### Low Severity Issues

#### 14. PillCell Styling Incomplete
**Location**: `packages/grafana-ui/src/components/Table/TableNG/Cells/PillCell.tsx:88`  
**Severity**: Low  
**Category**: Styling

**Description**:
Comment: `// FIXME: this does not yet support "shades of a color"`

PillCell styling doesn't support all color schemes available in other components.

**Recommendation**:
1. Implement shades color mode for consistency
2. Add tests covering all color modes
3. Update storybook examples

---

#### 15. Unused Route Conversion in Search Service
**Location**: `public/app/features/search/service/unified.ts:437`  
**Severity**: Low  
**Category**: Code Quality

**Description**:
Comment: `// 🤯 FIXME hit.name is k8s name, eg grafana dashboards UID`

Property mapping confusion between k8s resource names and dashboard properties.

**Recommendation**:
1. Clarify naming convention
2. Add comments documenting the distinction
3. Consider renaming variable for clarity:
```typescript
// Before
const name = hit.title; // FIXME: ...

// After
const displayName = hit.title; // hit.title is the dashboard title, not k8s name
```

---

## Issues Summary by Component

### Frontend (TypeScript/React)
- Concurrent request batching bug (useRuleSourcesWithRuler)
- Loading state tracking bug (useRuleSourcesWithRuler)
- Type safety (95+ `any[]` instances)
- String literal completion (Prometheus)
- PromQL parsing incomplete (multiple functions)
- Accessibility (Modal titles)
- Test coverage (RadialGauge)
- Architecture (Context usage)

### Code Generation
- DashboardLink type union issue
- API client placeholder TODOs

### Backend Validation
- Missing admission hook validators (provisioning)

### Code Organization
- Duplicated types (browse-dashboards)
- Hardcoded query types
- Test utilities missing

---

## Recommendations by Priority

### Immediate (This Sprint)
1. Fix concurrent request batching (Critical)
2. Fix loading state tracking (Critical)
3. Implement provisioning validation hooks (High)

### Short Term (Next 2 Sprints)
1. Audit and reduce `any[]` usage (High)
2. Fix DashboardLink type generation (High)
3. Complete PromQL string literal support (High)

### Medium Term (Next Quarter)
1. Remove duplicated types (Medium)
2. Fix Modal accessibility (Medium)
3. Add RadialGauge test coverage (Medium)

### Backlog
1. Refactor PromQL parser limitations
2. Improve Radial Gauge context architecture
3. Update PillCell color support
4. Clarify search service naming

---

## Testing Notes

- **Test-Driven Issues**: 2 bugs (batching, loading state) have existing failing tests that document expected behavior
- **Integration Tests Needed**: For provisioning validation, API client generation
- **Performance Tests Recommended**: For datasource discovery batching

---

## Files Modified/Analyzed

- `public/app/features/alerting/unified/hooks/useRuleSourcesWithRuler.test.tsx` - Bug documentation
- `packages/grafana-api-clients/` - Type generation issues
- `packages/grafana-prometheus/` - Parser completeness
- `packages/grafana-ui/` - Component issues
- `packages/grafana-data/` - Feature toggles

---

## Next Steps

1. Create GitHub issues for each critical/high item
2. Assign to responsible teams/squads
3. Add to sprint planning for prioritization
4. Track fixes against this report
5. Conduct follow-up review in 6 weeks
