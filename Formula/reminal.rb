class Reminal < Formula
  desc "Remote terminal access — secure, zero-config alternative to SSH"
  homepage "https://github.com/harshalgajjar/Reminal"
  version "2.0.7"
  license "AGPL-3.0-or-later"

  head do
    url "https://github.com/harshalgajjar/Reminal.git", branch: "main"
  end

  on_macos do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.7/reminal_2.0.7_darwin_arm64.tar.gz"
      sha256 "183afe0acec65af4aa27304d3a0b01d1d0f4d92419c4a024317ad0624ac388f2"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.7/reminal_2.0.7_darwin_amd64.tar.gz"
      sha256 "30e9e677a0e84f11c53db41c09e75f4571564398566668e5b195908c8da08380"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.7/reminal_2.0.7_linux_arm64.tar.gz"
      sha256 "daec86f6c824a439058ecae1c6db4f51a4705e229693db9ecdeb35fee1c78ce6"
    end
    on_intel do
      url "https://github.com/harshalgajjar/Reminal/releases/download/v2.0.7/reminal_2.0.7_linux_amd64.tar.gz"
      sha256 "f5ba740dc81b69e06d7ac13dda6dfd675191f0abf5a8084420edbb144c5d8af4"
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
