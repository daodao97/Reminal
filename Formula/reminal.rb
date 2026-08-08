class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.2"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.2/reminal_2.0.2_darwin_arm64.tar.gz"
      sha256 "73927881f276aacc93e194b45633a1cc7f4d00e7915e8c949e7dd90c0c64ce97"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.2/reminal_2.0.2_darwin_amd64.tar.gz"
      sha256 "33cf3e94c2efc56e244cf976b6718d56d168c97c8dee916ee8316e94701ba39d"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.2/reminal_2.0.2_linux_arm64.tar.gz"
      sha256 "6a2924d8e42aab88ce6d9b9ead4e2f4c568d70e13a4b15ddc81a0738fb56acf3"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.2/reminal_2.0.2_linux_amd64.tar.gz"
      sha256 "735d0436262f0d3bc460cca606519b25b54df4a883ef19404c85ea42b45b01b1"
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
