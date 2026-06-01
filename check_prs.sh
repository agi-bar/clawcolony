#!/bin/bash

# Check if branches correspond to open PRs
for branch in $(git branch -r | grep -v main | grep -v HEAD | sed 's/origin\///'); do
    echo "Checking branch: $branch"
    # Try to get PR number from branch name (assuming PR branches follow a pattern)
    pr_number=$(echo $branch | grep -oE '[0-9]+$' || echo "")
    if [ -n "$pr_number" ]; then
        echo "  Potential PR #$pr_number"
    fi
    echo "  Recent commits:"
    git log --oneline -5 "origin/$branch"
    echo "  ---"
done