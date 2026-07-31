class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.12.2"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.2/reminal_1.12.2_darwin_arm64.tar.gz"
      sha256 "57950243149b95264d1b391ab40e400e8f8ede1aca063b5681b2c746ef6e4936"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.2/reminal_1.12.2_darwin_amd64.tar.gz"
      sha256 "6d3664aa1a6878f6bf50af2ab999c6a9ff53cc27a92e7290c93225eea264618a"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.2/reminal_1.12.2_linux_arm64.tar.gz"
      sha256 "cf46f6249a56e38c73be282423d82a2d742bb1415765f2cb46e732d08df50e68"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.2/reminal_1.12.2_linux_amd64.tar.gz"
      sha256 "29f98f352b7cdd8814e84cd0345aeaaaef63d31a7a36b2f85fd8cac70e66c895"
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
