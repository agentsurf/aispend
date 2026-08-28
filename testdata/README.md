# Fixtures

Canned vendor responses, served by `--fixture testdata/` instead of the network.

Filenames are the request's host and path with `/` replaced by `_`:

    api.openai.com/v1/organization/projects
    → api.openai.com_v1_organization_projects.json

Edit them by hand. A malformed file produces an error naming the file and the
parse position, not a confusing mapping bug further down.

These are shaped after real vendor responses but contain no real account data,
no real project names and no real keys.
