package db

import (
	"context"
	"testing"

	"github.com/tonis2/foundry/internal/repository"
)

func TestChatSessionRepositoriesCRUD_Postgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	sess, err := CreateChatSession(ctx, pool, "chat-repositories-test-session", "")
	if err != nil {
		t.Fatalf("CreateChatSession() error = %v", err)
	}
	t.Cleanup(func() { _ = DeleteChatSession(context.Background(), pool, sess.ID) })

	local := newLocalGitRepo(t)
	localRepo := createTestRepository(t, pool, repository.Repository{
		Name:      "local-context-repo",
		LocalPath: &local,
	})

	remote := "https://github.com/foo/remote-context-repo.git"
	remoteRepo := createTestRepository(t, pool, repository.Repository{
		Name:      "remote-context-repo",
		RemoteURL: &remote,
	})

	t.Run("attaching a repository makes it appear in ListSessionRepositories", func(t *testing.T) {
		if err := AttachRepositoryToSession(ctx, pool, sess.ID, localRepo.ID); err != nil {
			t.Fatalf("AttachRepositoryToSession() error = %v", err)
		}
		t.Cleanup(func() { _ = DetachRepositoryFromSession(context.Background(), pool, sess.ID, localRepo.ID) })

		repos, err := ListSessionRepositories(ctx, pool, sess.ID)
		if err != nil {
			t.Fatalf("ListSessionRepositories() error = %v", err)
		}
		if len(repos) != 1 || repos[0].ID != localRepo.ID {
			t.Fatalf("ListSessionRepositories() = %+v, want single entry with id %d", repos, localRepo.ID)
		}
		if repos[0].LocalPath == nil || *repos[0].LocalPath != local {
			t.Fatalf("LocalPath = %v, want %q", repos[0].LocalPath, local)
		}
	})

	t.Run("attaching the same repository twice is a no-op", func(t *testing.T) {
		if err := AttachRepositoryToSession(ctx, pool, sess.ID, localRepo.ID); err != nil {
			t.Fatalf("first AttachRepositoryToSession() error = %v", err)
		}
		t.Cleanup(func() { _ = DetachRepositoryFromSession(context.Background(), pool, sess.ID, localRepo.ID) })
		if err := AttachRepositoryToSession(ctx, pool, sess.ID, localRepo.ID); err != nil {
			t.Fatalf("second AttachRepositoryToSession() error = %v", err)
		}

		repos, err := ListSessionRepositories(ctx, pool, sess.ID)
		if err != nil {
			t.Fatalf("ListSessionRepositories() error = %v", err)
		}
		if len(repos) != 1 {
			t.Fatalf("ListSessionRepositories() = %+v, want exactly one row after duplicate attach", repos)
		}
	})

	t.Run("a remote-only repository is listed safely with a nil LocalPath", func(t *testing.T) {
		if err := AttachRepositoryToSession(ctx, pool, sess.ID, remoteRepo.ID); err != nil {
			t.Fatalf("AttachRepositoryToSession() error = %v", err)
		}
		t.Cleanup(func() { _ = DetachRepositoryFromSession(context.Background(), pool, sess.ID, remoteRepo.ID) })

		repos, err := ListSessionRepositories(ctx, pool, sess.ID)
		if err != nil {
			t.Fatalf("ListSessionRepositories() error = %v", err)
		}
		var found *repository.Repository
		for i := range repos {
			if repos[i].ID == remoteRepo.ID {
				found = &repos[i]
			}
		}
		if found == nil {
			t.Fatalf("ListSessionRepositories() = %+v, want remote-only repository %d present", repos, remoteRepo.ID)
		}
		if found.LocalPath != nil {
			t.Fatalf("LocalPath = %v, want nil for remote-only repository", found.LocalPath)
		}
		if found.RemoteURL == nil || *found.RemoteURL != remote {
			t.Fatalf("RemoteURL = %v, want %q", found.RemoteURL, remote)
		}
	})

	t.Run("detaching removes the repository from the session's context", func(t *testing.T) {
		if err := AttachRepositoryToSession(ctx, pool, sess.ID, localRepo.ID); err != nil {
			t.Fatalf("AttachRepositoryToSession() error = %v", err)
		}
		if err := DetachRepositoryFromSession(ctx, pool, sess.ID, localRepo.ID); err != nil {
			t.Fatalf("DetachRepositoryFromSession() error = %v", err)
		}

		repos, err := ListSessionRepositories(ctx, pool, sess.ID)
		if err != nil {
			t.Fatalf("ListSessionRepositories() error = %v", err)
		}
		for _, r := range repos {
			if r.ID == localRepo.ID {
				t.Fatalf("ListSessionRepositories() = %+v, want id %d absent after detach", repos, localRepo.ID)
			}
		}
	})

	t.Run("detaching a repository that is not attached is a no-op", func(t *testing.T) {
		if err := DetachRepositoryFromSession(ctx, pool, sess.ID, localRepo.ID); err != nil {
			t.Fatalf("DetachRepositoryFromSession() error = %v", err)
		}
	})
}
