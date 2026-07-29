class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.10.1"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.1/reminal_1.10.1_darwin_arm64.tar.gz"
      sha256 "477752459f397a8e24077ce05b5e374394fd9ed8ee4d0ccc187cf6e2bf958ba7"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.1/reminal_1.10.1_darwin_amd64.tar.gz"
      sha256 "472cc0452a6ac94525845ee2ff8002536c1be9ddc6b77930c92166d219db7b72"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.1/reminal_1.10.1_linux_arm64.tar.gz"
      sha256 "7b40b7c1fcbf6320fd01e23c1a5467ab275db2f579d80787735c41c2206d68d8"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.10.1/reminal_1.10.1_linux_amd64.tar.gz"
      sha256 "c93a6d8860f51ce9f970e5488a853016ba6c730a0b46b9d4991473f76f3bc723"
    end
  end

  depends_on "go" => :build if build.head?

  def install
    if build.head?
      system "go", "build", "-ldflags=#{ldflags}", "-o", bin/"reminal", "./cmd/reminal"
      # Build the native window-capture helper from source when Xcode is present;
      # otherwise the window mirror falls back to screencapture.
      if OS.mac? && which("swiftc")
        system "swiftc", "-O", "-o", bin/"reminal-capture", "native/reminal-capture/main.swift"
      end
    else
      bin.install "reminal"
      # The darwin bottle bundles the ScreenCaptureKit capture helper next to the
      # binary; the agent auto-discovers it for the native window mirror.
      bin.install "reminal-capture" if File.exist?("reminal-capture")
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
