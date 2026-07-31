class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "1.12.0"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.0/reminal_1.12.0_darwin_arm64.tar.gz"
      sha256 "51b5767ae1d13637d7b31779b5e9b0af286a40bac37f74f6c0c3922fd1c0c579"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.0/reminal_1.12.0_darwin_amd64.tar.gz"
      sha256 "4a916e483fa0f77cdf983bc4f2a7065fc02df626c42abba763e3c32ac011afe8"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.0/reminal_1.12.0_linux_arm64.tar.gz"
      sha256 "1d3c34dee0ae092407adf18664a618fbb2340b3e95df40b8d264b7b89678e46d"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v1.12.0/reminal_1.12.0_linux_amd64.tar.gz"
      sha256 "f952dc2c97ae1876681ca3cbdcdb7dd57d9420144b5c57fb206f914c0837c546"
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
