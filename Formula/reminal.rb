class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.9.5"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.5/reminal_1.9.5_darwin_arm64.tar.gz"
      sha256 "7277b764bb39bc0eb66d895d05369320ecea4bae526425058a6c036093c57d6d"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.5/reminal_1.9.5_darwin_amd64.tar.gz"
      sha256 "8b5c25f92141d4d9045b48cab0e44384a8ea33eb1bac1a355a92e62ab7ff1594"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.5/reminal_1.9.5_linux_arm64.tar.gz"
      sha256 "9e3bf25373ed4d1766d11d9c14303b85c351b4ec5addf33f9a215aef58e7b1c5"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.9.5/reminal_1.9.5_linux_amd64.tar.gz"
      sha256 "edbf342dbc013305a3d96e131e16b131698f2874a6732d10c0fa4b1db1e9c4ae"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
    else
      bin.install "reminal"
    end
  end

  def ldflags
    "-s -w " \
      "-X main.version=#{version} " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudRelay=wss://reminal-relay.futuristic.workers.dev/ws " \
      "-X github.com/reminal/reminal/internal/config.DefaultCloudWeb=https://reminal-relay.futuristic.workers.dev"
  end

  def caveats
    <<~EOS
      reminal connects to the hosted relay automatically — no setup needed.

        reminal              # share your terminal
        reminal --connect ID --pin PIN
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/reminal version")
  end
end
